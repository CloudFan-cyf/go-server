package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"sync"
	"time"

	pb "Go-server/generated/remote_control_gRPC"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

var (
	tls      = flag.Bool("tls", false, "Connection uses TLS if true, else plain TCP")
	certFile = flag.String("cert_file", "", "The TLS cert file")
)

// 连接列表
var connectedUsers = make(map[string]*pb.UserId)
var connectedRobots = make(map[string]*pb.RobotId)

// 指令队列和执行结果队列
var commandQueues = make(map[string]chan *pb.CommandRequest)
var commandResponseQueues = make(map[string]chan *pb.CommandResponse, 10)

// 视频相关队列
var subscriptionNum = make(map[string]int) // 每个RobotId对应一个订阅数量
var videoQueues = make(map[string]chan *pb.VideoFrame, 60)

var mu sync.Mutex

///////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////
// UserClientService

// userClientServer 是 UserClientService 的实现
type userClientServer struct {
	pb.UnimplementedUserClientServiceServer
}

// UserClientService中SendCommand 实现
// 服务端从用户端以SimpleRPC的方法接收一条指令CommandRequest，
// 若已连接机器人列表中无对应的机器人，则返回错误
// 放入机器人指令队列，等待RobotClientService的PullCommand方法取出指令执行。
// 并从机器人处接收指令的执行结果（等待执行结果需要有超时机制）
// 再将结果CommandResponse返回给用户。
func (s *userClientServer) SendCommand(ctx context.Context, req *pb.CommandRequest) (*pb.CommandResponse, error) {
	// 检查机器人是否已连接
	mu.Lock()
	_, ok := connectedRobots[req.RobotId.Id]
	mu.Unlock()

	if !ok {
		return nil, errors.New("robot not connected")
	}

	// 检查指令队列是否存在
	mu.Lock()
	commandQueue, exists := commandQueues[req.RobotId.Id]
	if !exists {
		return nil, errors.New("robot not connected,or command queue not exist")
	}
	commandResponseQueue, responseExists := commandResponseQueues[req.RobotId.Id]
	if !responseExists {
		return nil, errors.New("robot not connected,or command response queue not exist")
	}
	mu.Unlock()

	// 清空指令队列并放入新的指令
	// 由于SendCommand方法是用来发送重要指令的，因此需要清空指令队列，保证一定能够被写入
	mu.Lock()
	for len(commandQueue) > 0 {
		<-commandQueue
	}
	mu.Unlock()
	commandQueue <- req
	log.Printf("Command added to queue for robot ID %s: Seq ID %d", req.RobotId.Id, req.SeqId)

	// 创建带有超时的上下文
	ctxWithTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	select {
	case <-ctxWithTimeout.Done():
		return nil, errors.New("command execution timed out")
	case res := <-commandResponseQueue:
		if res.SeqId == req.SeqId {
			return res, nil
		}
	}
	return nil, errors.New("some unknown error occurred")
}

// UserClientService中PushCommand 实现（双向流式传输）
// 服务端从用户端以BidirectionalStreaming RPC的方法接收用户的多条指令CommandRequest，
// 服务端将流中的指令按序加入CommanRequest.RobotId.Id对应的指令队列，用来缓冲用户的的指令，
// 应有多条指令队列，对应每一个已连接的机器人（即在每个机器人连接时建立对应的指令队列和执行结果队列）
// 服务端从CommandRequest.RobotId.Id对应的指令队列中取出指令，按序执行，并将执行结果CommandResponse返回给用户。
func (s *userClientServer) PushCommand(stream pb.UserClientService_PushCommandServer) error {
	for {
		req, err := stream.Recv()
		if err != nil {
			log.Printf("Error receiving command: %v", err)
			return err
		}
		log.Printf("Received stream command for robot ID %s: Seq ID %d, Command: %v", req.RobotId.Id, req.SeqId, req.Command)

		mu.Lock()
		commandQueue, exists := commandQueues[req.RobotId.Id]
		if !exists {
			return errors.New("robot not connected,or command queue not exist")
		}
		commandResponseQueue, responseExists := commandResponseQueues[req.RobotId.Id]
		if !responseExists {
			return errors.New("robot not connected,or command response queue not exist")
		}
		mu.Unlock()

		// 写入指令队列（非阻塞写入）
		select {
		case commandQueue <- req:
			log.Printf("Command added to queue for robot ID %s: Seq ID %d", req.RobotId.Id, req.SeqId)
		default:
			log.Printf("Command queue for robot ID %s is full, dropping command: Seq ID %d", req.RobotId.Id, req.SeqId)
			return errors.New("command queue is full, command dropped")
		}

		// 返回指令执行结果（异步处理）
		go func(req *pb.CommandRequest, stream pb.UserClientService_PushCommandServer) error {
			ctxWithTimeout, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for {
				select {
				case <-ctxWithTimeout.Done():
					log.Printf("Command execution timed out for robot ID %s: Seq ID %d", req.RobotId.Id, req.SeqId)
					return errors.New("command execution timed out")
				case res := <-commandResponseQueue:
					if res.SeqId == req.SeqId {
						if err := stream.Send(res); err != nil {
							log.Printf("Error sending response: %v", err)
						}
						return errors.New("some unknown error occurred")
					}
				}
			}
		}(req, stream)
	}
}

// CancelVideoSubscription 实现
// 用户端取消订阅某个机器人的视频流
// 服务端在接收到取消订阅请求后，将该机器人的订阅状态减一，并关闭对应的视频队列，
func (s *userClientServer) CancelVideoSubscription(ctx context.Context, req *pb.CancelVideoSubscriptionRequest) (*emptypb.Empty, error) {
	log.Printf("Cancelling video subscription for robot ID %s", req.RobotId.Id)

	mu.Lock()
	if subscriptionNum[req.RobotId.Id] > 0 {
		subscriptionNum[req.RobotId.Id] -= 1
		log.Printf("Decremented subscription count for robot ID %s, new count: %d", req.RobotId.Id, subscriptionNum[req.RobotId.Id])
	} else {
		log.Printf("No active subscriptions for robot ID %s to cancel", req.RobotId.Id)
	}
	mu.Unlock()

	return &emptypb.Empty{}, nil
}

// PullVideoStream 实现（流式传输视频帧）
// 服务端接收用户端的PullVideoRequest请求（订阅某个机器人的视频流），返回视频流数据。
// 服务端在接收用户的订阅请求后，根据PullVideoRequest中的RobotId.Id和CamId，向机器人客户端发送订阅通知
// 机器人客户端收到订阅通知后，开始向服务端推送视频流数据，
// 服务端使用队列来缓冲视频帧，供用户端PullVideoStream方法取出。
func (s *userClientServer) PullVideoStream(req *pb.PullVideoRequest, stream pb.UserClientService_PullVideoStreamServer) error {
	log.Printf("Starting video stream for robot ID %s, camera ID %d", req.RobotId.Id, req.CamId)

	// 检查视频队列是否存在
	mu.Lock()
	queue, exists := videoQueues[req.RobotId.Id]
	if !exists {
		mu.Unlock()
		return errors.New("video stream not available for robot")
	}
	subscriptionNum[req.RobotId.Id] += 1
	mu.Unlock()

	// 等待队列中有数据再开始取数据-发送过程，加入超时机制
	for {
		ctxWithTimeout, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		select {
		case <-ctxWithTimeout.Done():
			log.Printf("Timeout waiting for video frame for robot ID %s", req.RobotId.Id)
			return errors.New("timeout waiting for video frame")
		case frame, ok := <-queue:
			if !ok {
				log.Printf("Video queue closed for robot ID %s", req.RobotId.Id)
				return errors.New("video stream closed")
			}
			if err := stream.Send(frame); err != nil {
				log.Printf("Error sending video frame: %v", err)
				return err
			}
		}
	}
}

// PullStatus 实现
func (s *userClientServer) PullStatus(req *pb.PullStatusRequest, stream pb.UserClientService_PullStatusServer) error {
	log.Printf("Pulling status for robot ID %s", req.RobotId.Id)
	for i := 0; i < 10; i++ {
		status := &pb.Status{
			Status: &pb.Status_BatteryLevel{
				BatteryLevel: 80.0,
			},
		}
		if err := stream.Send(status); err != nil {
			log.Printf("Error sending status: %v", err)
			return err
		}
	}
	return nil
}

// Ping 实现
func (s *userClientServer) Ping(ctx context.Context, req *emptypb.Empty) (*emptypb.Empty, error) {
	log.Println("Ping received from client")
	return &emptypb.Empty{}, nil
}

// SendAuthentications 实现
// 用户端连接后发送的第一个请求，用于确认用户ID
func (s *userClientServer) SendAuthentications(ctx context.Context, req *pb.UserId) (*emptypb.Empty, error) {
	log.Printf("Received authentication for user ID %s", req.Id)

	mu.Lock()
	connectedUsers[req.Id] = req
	mu.Unlock()

	return &emptypb.Empty{}, nil
}

///////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////
// RobotClientService

// robotClientServer 是 RobotClientService 的实现
type robotClientServer struct {
	pb.UnimplementedRobotClientServiceServer
}

// SubscribeNotify 实现
// 机器人连接认证后发送的请求，用于请求建立订阅通知流
// 服务端在RobotId对应的videoSubscribers变为true时，向机器人客户端发送订阅通知，
// 机器人客户端收到订阅通知后，开始向服务端推送视频流数据。
func (s *robotClientServer) SubscribeNotify(req *pb.ListenToSubscriptionRequest, stream pb.RobotClientService_SubscribeNotifyServer) error {
	robotId := req.RobotId.Id
	log.Printf("Robot ID %s is requesting subscription notifications", robotId)

	for {
		// 监听订阅状态变化
		mu.Lock()
		subscribed := subscriptionNum[robotId]
		mu.Unlock()

		if subscribed != 0 {
			notification := &pb.SubscribedNotification{
				IsSubscribed: true,
			}
			if err := stream.Send(notification); err != nil {
				log.Printf("Error sending subscribed notification to robot ID %s: %v", robotId, err)
				return err
			}
		}
		time.Sleep(1 * time.Second) // 避免过于频繁的轮询
	}
}

// PushVideoStream 实现
// 服务端从机器人端以BidirectionalStreaming RPC的方法接收机器人的连续JPEG视频帧VideoFrame，
// 将其放入视频帧缓冲队列中，供用户端PullVideoStream方法取出。
// 视频流的推送和接收使用订阅制，即用户端向服务器端请求某个RobotId的视频流，服务器端才开始推送视频流。
// 服务端在每个RobotId对应的videoSubscribers变为true时，向机器人客户端发送订阅通知，
// 机器人客户端收到订阅通知后，开始向服务端推送视频流数据。
// PushVideoStream 实现
func (s *robotClientServer) PushVideoStream(stream pb.RobotClientService_PushVideoStreamServer) error {
	for {
		frameData, err := stream.Recv()
		if err != nil {
			log.Printf("Error receiving video frame: %v", err)
			return err
		}
		robotId := frameData.RobotId.Id
		frame := frameData.Frame

		// 存入对应的机器人视频队列
		mu.Lock()
		videoQueue, exists := videoQueues[robotId]
		if exists {
			// 当队列已满时，弹出队头元素
			if len(videoQueue) == cap(videoQueue) {
				<-videoQueue
			}
			videoQueue <- frame
			log.Printf("Added video frame to queue for robot ID %s", robotId)
		} else {
			return errors.New("video stream not available for robot")
		}
		mu.Unlock()
	}
}

// PullCommand 实现
func (s *robotClientServer) PullCommand(stream pb.RobotClientService_PullCommandServer) error {
	for i := 0; i < 10; i++ {
		command := &pb.CommandRequest{
			SeqId:   uint32(i),
			Stamp:   uint64(i * 1000),
			RobotId: &pb.RobotId{Id: "robot_1"},
		}
		if err := stream.Send(command); err != nil {
			log.Printf("Error sending command: %v", err)
			return err
		}
	}
	return nil
}

// PushStatus 实现
func (s *robotClientServer) PushStatus(stream pb.RobotClientService_PushStatusServer) error {
	for {
		status, err := stream.Recv()
		if err != nil {
			log.Printf("Error receiving status: %v", err)
			return err
		}
		log.Printf("Received status: %v", status)
	}
}

// Ping 实现
func (s *robotClientServer) Ping(ctx context.Context, req *emptypb.Empty) (*emptypb.Empty, error) {
	log.Println("Ping received from client")
	return &emptypb.Empty{}, nil
}

// SendAuthentications 实现
// Robot端连接后发送的第一个请求，用于确认RobotID
// 服务端在接收到认证请求后，将该机器人加入连接列表，并建立对应的指令队列、执行结果队列和视频队列、订阅状态
func (s *robotClientServer) SendAuthentications(ctx context.Context, req *pb.RobotId) (*emptypb.Empty, error) {
	rbtId := req.Id
	log.Printf("Received authentication for robot ID %s", rbtId)
	// 将该机器人加入连接列表，并建立对应的指令队列、执行结果队列和视频队列、订阅状态
	mu.Lock()
	if _, exists := connectedRobots[rbtId]; exists {
		log.Printf("Robot ID %s is already connected", rbtId)
		mu.Unlock()
		return &emptypb.Empty{}, nil
	}
	connectedRobots[rbtId] = req
	commandQueues[rbtId] = make(chan *pb.CommandRequest, 10)
	commandResponseQueues[rbtId] = make(chan *pb.CommandResponse, 10)
	videoQueues[rbtId] = make(chan *pb.VideoFrame, 60)
	subscriptionNum[rbtId] = 0
	mu.Unlock()
	return &emptypb.Empty{}, nil
}

func main() {
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()

	// 注册服务
	pb.RegisterUserClientServiceServer(grpcServer, &userClientServer{})
	pb.RegisterRobotClientServiceServer(grpcServer, &robotClientServer{})

	log.Println("gRPC server running on port 50051...")
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
