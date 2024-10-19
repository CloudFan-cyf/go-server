package server

import (
	"context"
	"log"
	"net"

	pb "Go-server/generated/remote_control_gRPC"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

// server 是 UserClientService 的实现
type userClientServer struct {
	pb.UnimplementedUserClientServiceServer
}

// SendCommand 实现
func (s *userClientServer) SendCommand(ctx context.Context, req *pb.CommandRequest) (*pb.CommandResponse, error) {
	log.Printf("Received command for robot ID %s: Seq ID %d, Command: %v", req.RobotId.Id, req.SeqId, req.Command)
	return &pb.CommandResponse{
		SeqId:     req.SeqId,
		Stamp:     req.Stamp,
		IsSuccess: true,
	}, nil
}

// PushCommand 实现（双向流式传输）
func (s *userClientServer) PushCommand(stream pb.UserClientService_PushCommandServer) error {
	for {
		req, err := stream.Recv()
		if err != nil {
			log.Printf("Error receiving command: %v", err)
			return err
		}
		log.Printf("Received stream command for robot ID %s: Seq ID %d, Command: %v", req.RobotId.Id, req.SeqId, req.Command)

		// 返回响应
		response := &pb.CommandResponse{
			SeqId:     req.SeqId,
			Stamp:     req.Stamp,
			IsSuccess: true,
		}
		if err := stream.Send(response); err != nil {
			log.Printf("Error sending response: %v", err)
			return err
		}
	}
}

// PullVideoStream 实现（流式传输视频帧）
func (s *userClientServer) PullVideoStream(req *pb.PullVideoRequest, stream pb.UserClientService_PullVideoStreamServer) error {
	log.Printf("Starting video stream for robot ID %s, camera ID %d", req.RobotId.Id, req.CamId)
	// 模拟视频流数据
	for i := 0; i < 10; i++ {
		frame := &pb.VideoFrame{
			Data: []byte("Mock video frame data"),
		}
		if err := stream.Send(frame); err != nil {
			log.Printf("Error sending video frame: %v", err)
			return err
		}
	}
	return nil
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
func (s *userClientServer) SendAuthentications(ctx context.Context, req *pb.UserId) (*emptypb.Empty, error) {
	log.Printf("Received authentication for user ID %d", req.Id)
	return &emptypb.Empty{}, nil
}

// server 是 RobotClientService 的实现
type robotClientServer struct {
	pb.UnimplementedRobotClientServiceServer
}

// PushVideoStream 实现
func (s *robotClientServer) PushVideoStream(stream pb.RobotClientService_PushVideoStreamServer) error {
	for {
		frame, err := stream.Recv()
		if err != nil {
			log.Printf("Error receiving video frame: %v", err)
			return err
		}
		log.Printf("Received video frame of length %d", len(frame.Data))
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

// Ping 和 SendAuthentications 方法与 UserClientService 类似，可以复用实现。

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
