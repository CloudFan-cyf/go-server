package main

import (
	"context"
	"log"
	"net"
	"testing"

	pb "Go-server/generated/remote_control_gRPC"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

var lis *bufconn.Listener

func init() {
	lis = bufconn.Listen(bufSize)
	grpcServer := grpc.NewServer()

	// 注册服务
	pb.RegisterUserClientServiceServer(grpcServer, &userClientServer{})
	pb.RegisterRobotClientServiceServer(grpcServer, &robotClientServer{})

	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("Failed to serve: %v", err)
		}
	}()
}

func bufDialer(context.Context, string) (net.Conn, error) {
	return lis.Dial()
}

func TestSendAuthentications(t *testing.T) {
	ctx := context.Background()
	conn, err := grpc.NewClient("localhost:50051", grpc.WithContextDialer(bufDialer), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to dial bufnet: %v", err)
	}
	defer conn.Close()

	client := pb.NewUserClientServiceClient(conn)
	userId := &pb.UserId{Id: "user_123"}

	_, err = client.SendAuthentications(ctx, userId)
	if err != nil {
		t.Fatalf("SendAuthentications failed: %v", err)
	}
}

func TestPullVideoStream(t *testing.T) {
	ctx := context.Background()
	conn, err := grpc.NewClient("localhost:50051", grpc.WithContextDialer(bufDialer), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to dial bufnet: %v", err)
	}
	defer conn.Close()

	userClient := pb.NewUserClientServiceClient(conn)
	robotClient := pb.NewRobotClientServiceClient(conn)

	robotId := &pb.RobotId{Id: "robot_3"}
	_, err = robotClient.SendAuthentications(ctx, robotId)
	if err != nil {
		t.Fatalf("Robot SendAuthentications failed: %v", err)
	}

	pullVideoReq := &pb.PullVideoRequest{
		RobotId: robotId,
		CamId:   1,
	}

	stream, err := userClient.PullVideoStream(ctx, pullVideoReq)
	if err != nil {
		t.Fatalf("PullVideoStream failed to create stream: %v", err)
	}

	for i := 0; i < 3; i++ {
		frame, err := stream.Recv()
		if err != nil {
			t.Fatalf("Failed to receive video frame: %v", err)
		}
		if len(frame.Data) == 0 {
			t.Errorf("Expected video frame data, got empty frame")
		}
	}
}

func TestPushVideoStream(t *testing.T) {
	ctx := context.Background()
	conn, err := grpc.NewClient("localhost:50051", grpc.WithContextDialer(bufDialer), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to dial bufnet: %v", err)
	}
	defer conn.Close()

	robotClient := pb.NewRobotClientServiceClient(conn)
	robotId := &pb.RobotId{Id: "robot_5"}
	_, err = robotClient.SendAuthentications(ctx, robotId)
	if err != nil {
		t.Fatalf("Robot SendAuthentications failed: %v", err)
	}

	stream, err := robotClient.PushVideoStream(ctx)
	if err != nil {
		t.Fatalf("PushVideoStream failed to create stream: %v", err)
	}

	frameData := &pb.VideoFrameData{
		RobotId: robotId,
		Frame:   &pb.VideoFrame{Data: []byte("test frame data")},
	}

	err = stream.Send(frameData)
	if err != nil {
		t.Fatalf("Failed to send video frame: %v", err)
	}
}

func TestCancelVideoSubscription(t *testing.T) {
	ctx := context.Background()
	conn, err := grpc.NewClient("localhost:50051", grpc.WithContextDialer(bufDialer), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to dial bufnet: %v", err)
	}
	defer conn.Close()

	userClient := pb.NewUserClientServiceClient(conn)
	robotClient := pb.NewRobotClientServiceClient(conn)

	robotId := &pb.RobotId{Id: "robot_4"}
	_, err = robotClient.SendAuthentications(ctx, robotId)
	if err != nil {
		t.Fatalf("Robot SendAuthentications failed: %v", err)
	}

	cancelReq := &pb.CancelVideoSubscriptionRequest{
		RobotId: robotId,
	}

	_, err = userClient.CancelVideoSubscription(ctx, cancelReq)
	if err != nil {
		t.Fatalf("CancelVideoSubscription failed: %v", err)
	}
}

func TestSubscribeNotify(t *testing.T) {
	ctx := context.Background()
	conn, err := grpc.NewClient("localhost:50051", grpc.WithContextDialer(bufDialer), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to dial bufnet: %v", err)
	}
	defer conn.Close()

	robotClient := pb.NewRobotClientServiceClient(conn)
	robotId := &pb.RobotId{Id: "robot_6"}
	_, err = robotClient.SendAuthentications(ctx, robotId)
	if err != nil {
		t.Fatalf("Robot SendAuthentications failed: %v", err)
	}

	subReq := &pb.ListenToSubscriptionRequest{RobotId: robotId}
	stream, err := robotClient.SubscribeNotify(ctx, subReq)
	if err != nil {
		t.Fatalf("SubscribeNotify failed to create stream: %v", err)
	}

	notification, err := stream.Recv()
	if err != nil {
		t.Fatalf("Failed to receive subscription notification: %v", err)
	}

	if !notification.IsSubscribed {
		t.Errorf("Expected subscription notification to be true, got false")
	}
}
