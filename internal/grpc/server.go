package grpc

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net"
	"time"

	"chat-app/internal/database"
	"chat-app/internal/models"
	"chat-app/internal/redis"
	pb "chat-app/proto"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type ChatServer struct {
	pb.UnimplementedChatServiceServer
	db    *database.DB
	redis *redis.RedisClient
}

// NewChatServer creates a new chat server
func NewChatServer(db *database.DB, redis *redis.RedisClient) *ChatServer {
	return &ChatServer{
		db:    db,
		redis: redis,
	}
}

// SendMessage handles sending a message
func (s *ChatServer) SendMessage(ctx context.Context, msg *pb.Message) (*pb.MessageResponse, error) {
	messageID := uuid.New().String()
	timestamp := time.Now()

	// Store message in database
	query := `INSERT INTO messages (id, user_id, username, room_id, content, message_type, timestamp, metadata) 
			  VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	
	_, err := s.db.ExecContext(ctx, query, 
		messageID, msg.UserId, msg.Username, msg.RoomId, 
		msg.Content, msg.MessageType, timestamp, msg.Metadata)
	
	if err != nil {
		log.Printf("Error storing message: %v", err)
		return &pb.MessageResponse{
			Success: false,
			Error:   "Failed to store message",
		}, status.Error(codes.Internal, "Failed to store message")
	}

	// Publish message to Redis for real-time delivery
	messageData := map[string]interface{}{
		"id":           messageID,
		"user_id":      msg.UserId,
		"username":     msg.Username,
		"room_id":      msg.RoomId,
		"content":      msg.Content,
		"message_type": msg.MessageType,
		"timestamp":    timestamp.Unix(),
		"metadata":     msg.Metadata,
	}

	channel := fmt.Sprintf("room:%s", msg.RoomId)
	if err := s.redis.Publish(ctx, channel, messageData); err != nil {
		log.Printf("Error publishing message: %v", err)
	}

	return &pb.MessageResponse{
		Success:   true,
		MessageId: messageID,
	}, nil
}

// GetMessageHistory retrieves message history for a room
func (s *ChatServer) GetMessageHistory(ctx context.Context, req *pb.HistoryRequest) (*pb.HistoryResponse, error) {
	query := `SELECT id, user_id, username, room_id, content, message_type, timestamp, metadata 
			  FROM messages 
			  WHERE room_id = $1 
			  ORDER BY timestamp DESC 
			  LIMIT $2`

	if req.BeforeTimestamp > 0 {
		query = `SELECT id, user_id, username, room_id, content, message_type, timestamp, metadata 
				 FROM messages 
				 WHERE room_id = $1 AND timestamp < $2
				 ORDER BY timestamp DESC 
				 LIMIT $3`
	}

	var rows *sql.Rows
	var err error

	if req.BeforeTimestamp > 0 {
		rows, err = s.db.QueryContext(ctx, query, req.RoomId, time.Unix(req.BeforeTimestamp, 0), req.Limit)
	} else {
		rows, err = s.db.QueryContext(ctx, query, req.RoomId, req.Limit)
	}

	if err != nil {
		log.Printf("Error querying messages: %v", err)
		return nil, status.Error(codes.Internal, "Failed to retrieve messages")
	}
	defer rows.Close()

	var messages []*pb.Message
	for rows.Next() {
		var msg pb.Message
		var timestamp time.Time
		var metadataJSON []byte

		err := rows.Scan(&msg.Id, &msg.UserId, &msg.Username, &msg.RoomId, 
			&msg.Content, &msg.MessageType, &timestamp, &metadataJSON)
		
		if err != nil {
			log.Printf("Error scanning message: %v", err)
			continue
		}

		msg.Timestamp = timestamp.Unix()
		// Parse metadata if needed
		if len(metadataJSON) > 0 {
			// Simple metadata parsing - in production, use proper JSON unmarshaling
			msg.Metadata = make(map[string]string)
		}

		messages = append(messages, &msg)
	}

	hasMore := len(messages) == int(req.Limit)

	return &pb.HistoryResponse{
		Messages: messages,
		HasMore:  hasMore,
	}, nil
}

// JoinRoom handles joining a room
func (s *ChatServer) JoinRoom(ctx context.Context, req *pb.RoomRequest) (*pb.RoomResponse, error) {
	// Add user to room members
	query := `INSERT INTO room_members (room_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`
	_, err := s.db.ExecContext(ctx, query, req.RoomId, req.UserId)
	
	if err != nil {
		log.Printf("Error joining room: %v", err)
		return &pb.RoomResponse{
			Success: false,
			Error:   "Failed to join room",
		}, status.Error(codes.Internal, "Failed to join room")
	}

	// Update user status to online
	updateQuery := `UPDATE users SET status = 'online', last_seen = NOW() WHERE id = $1`
	_, err = s.db.ExecContext(ctx, updateQuery, req.UserId)
	
	if err != nil {
		log.Printf("Error updating user status: %v", err)
	}

	// Add user to Redis set for online users in this room
	roomKey := fmt.Sprintf("room:%s:users", req.RoomId)
	if err := s.redis.SAdd(ctx, roomKey, req.UserId); err != nil {
		log.Printf("Error adding user to Redis: %v", err)
	}

	return &pb.RoomResponse{
		Success: true,
	}, nil
}

// LeaveRoom handles leaving a room
func (s *ChatServer) LeaveRoom(ctx context.Context, req *pb.RoomRequest) (*pb.RoomResponse, error) {
	// Remove user from room members
	query := `DELETE FROM room_members WHERE room_id = $1 AND user_id = $2`
	_, err := s.db.ExecContext(ctx, query, req.RoomId, req.UserId)
	
	if err != nil {
		log.Printf("Error leaving room: %v", err)
		return &pb.RoomResponse{
			Success: false,
			Error:   "Failed to leave room",
		}, status.Error(codes.Internal, "Failed to leave room")
	}

	// Remove user from Redis set
	roomKey := fmt.Sprintf("room:%s:users", req.RoomId)
	if err := s.redis.SRem(ctx, roomKey, req.UserId); err != nil {
		log.Printf("Error removing user from Redis: %v", err)
	}

	return &pb.RoomResponse{
		Success: true,
	}, nil
}

// GetOnlineUsers retrieves online users in a room
func (s *ChatServer) GetOnlineUsers(ctx context.Context, req *pb.OnlineUsersRequest) (*pb.OnlineUsersResponse, error) {
	// Get online users from Redis first
	roomKey := fmt.Sprintf("room:%s:users", req.RoomId)
	userIDs, err := s.redis.SMembers(ctx, roomKey)
	
	if err != nil {
		log.Printf("Error getting online users from Redis: %v", err)
		// Fallback to database
		return s.getOnlineUsersFromDB(ctx, req.RoomId)
	}

	if len(userIDs) == 0 {
		return &pb.OnlineUsersResponse{Users: []*pb.User{}}, nil
	}

	// Get user details from database
	query := `SELECT id, username, status, last_seen FROM users WHERE id = ANY($1)`
	rows, err := s.db.QueryContext(ctx, query, userIDs)
	
	if err != nil {
		log.Printf("Error querying users: %v", err)
		return nil, status.Error(codes.Internal, "Failed to retrieve users")
	}
	defer rows.Close()

	var users []*pb.User
	for rows.Next() {
		var user pb.User
		var lastSeen time.Time
		
		err := rows.Scan(&user.Id, &user.Username, &user.Status, &lastSeen)
		if err != nil {
			log.Printf("Error scanning user: %v", err)
			continue
		}
		
		user.LastSeen = lastSeen.Unix()
		users = append(users, &user)
	}

	return &pb.OnlineUsersResponse{Users: users}, nil
}

// getOnlineUsersFromDB fallback method to get online users from database
func (s *ChatServer) getOnlineUsersFromDB(ctx context.Context, roomID string) (*pb.OnlineUsersResponse, error) {
	query := `SELECT u.id, u.username, u.status, u.last_seen 
			  FROM users u 
			  JOIN room_members rm ON u.id = rm.user_id 
			  WHERE rm.room_id = $1 AND u.status = 'online'`
	
	rows, err := s.db.QueryContext(ctx, query, roomID)
	if err != nil {
		return nil, status.Error(codes.Internal, "Failed to retrieve users")
	}
	defer rows.Close()

	var users []*pb.User
	for rows.Next() {
		var user pb.User
		var lastSeen time.Time
		
		err := rows.Scan(&user.Id, &user.Username, &user.Status, &lastSeen)
		if err != nil {
			continue
		}
		
		user.LastSeen = lastSeen.Unix()
		users = append(users, &user)
	}

	return &pb.OnlineUsersResponse{Users: users}, nil
}

// StreamMessages streams messages for real-time updates
func (s *ChatServer) StreamMessages(req *pb.StreamRequest, stream pb.ChatService_StreamMessagesServer) error {
	ctx := stream.Context()
	channel := fmt.Sprintf("room:%s", req.RoomId)

	// Subscribe to Redis channel
	pubsub := s.redis.Subscribe(ctx, channel)
	defer pubsub.Close()

	// Send initial connection message
	initialMsg := &pb.Message{
		Id:          uuid.New().String(),
		UserId:      "system",
		Username:    "System",
		RoomId:      req.RoomId,
		Content:     "Connected to message stream",
		MessageType: "system",
		Timestamp:   time.Now().Unix(),
	}

	if err := stream.Send(initialMsg); err != nil {
		return status.Error(codes.Internal, "Failed to send initial message")
	}

	// Listen for messages
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			msg, err := pubsub.ReceiveMessage(ctx)
			if err != nil {
				log.Printf("Error receiving message: %v", err)
				continue
			}

			// Parse message and send to client
			// In a real implementation, you'd parse the JSON message
			// For now, we'll create a simple message
			streamMsg := &pb.Message{
				Id:          uuid.New().String(),
				UserId:      "user",
				Username:    "User",
				RoomId:      req.RoomId,
				Content:     msg.Payload,
				MessageType: "text",
				Timestamp:   time.Now().Unix(),
			}

			if err := stream.Send(streamMsg); err != nil {
				return status.Error(codes.Internal, "Failed to send message")
			}
		}
	}
}

// StartGRPCServer starts the gRPC server
func StartGRPCServer(db *database.DB, redis *redis.RedisClient, port string) error {
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return fmt.Errorf("failed to listen: %v", err)
	}

	server := grpc.NewServer()
	pb.RegisterChatServiceServer(server, NewChatServer(db, redis))

	log.Printf("gRPC server listening on port %s", port)
	return server.Serve(lis)
}
