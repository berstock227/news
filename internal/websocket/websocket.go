package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"chat-app/internal/database"
	"chat-app/internal/models"
	"chat-app/internal/redis"

	"github.com/gorilla/websocket"
	"github.com/google/uuid"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for development
	},
}

type WebSocketHandler struct {
	db    *database.DB
	redis *redis.RedisClient
	hub   *models.Hub
	mu    sync.RWMutex
}

type WSMessage struct {
	Type      string                 `json:"type"`
	UserID    string                 `json:"user_id"`
	Username  string                 `json:"username"`
	RoomID    string                 `json:"room_id"`
	Content   string                 `json:"content"`
	MessageID string                 `json:"message_id,omitempty"`
	Timestamp int64                  `json:"timestamp"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

type WSConnection struct {
	*models.Connection
	wsConn *websocket.Conn
}

// NewWebSocketHandler creates a new WebSocket handler
func NewWebSocketHandler(db *database.DB, redis *redis.RedisClient) *WebSocketHandler {
	hub := models.NewHub()
	handler := &WebSocketHandler{
		db:    db,
		redis: redis,
		hub:   hub,
	}

	// Start the hub
	go hub.Run()

	// Start Redis message listener
	go handler.listenRedisMessages()

	return handler
}

// HandleWebSocket handles WebSocket connections
func (h *WebSocketHandler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Get user info from query parameters (in production, use JWT tokens)
	userID := r.URL.Query().Get("user_id")
	username := r.URL.Query().Get("username")
	roomID := r.URL.Query().Get("room_id")

	if userID == "" || username == "" || roomID == "" {
		http.Error(w, "Missing required parameters", http.StatusBadRequest)
		return
	}

	// Upgrade HTTP connection to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Error upgrading connection: %v", err)
		return
	}

	// Create connection
	wsConn := &WSConnection{
		Connection: models.NewConnection(userID, username, roomID, conn, h.hub),
		wsConn:     conn,
	}

	// Register connection
	h.hub.Register <- wsConn.Connection

	// Send welcome message
	welcomeMsg := WSMessage{
		Type:      "system",
		UserID:    "system",
		Username:  "System",
		RoomID:    roomID,
		Content:   fmt.Sprintf("Welcome %s to room %s", username, roomID),
		MessageID: uuid.New().String(),
		Timestamp: time.Now().Unix(),
	}

	if err := wsConn.sendMessage(welcomeMsg); err != nil {
		log.Printf("Error sending welcome message: %v", err)
	}

	// Start goroutines for reading and writing
	go h.readPump(wsConn)
	go h.writePump(wsConn)
}

// readPump reads messages from the WebSocket connection
func (h *WebSocketHandler) readPump(conn *WSConnection) {
	defer func() {
		h.hub.Unregister <- conn.Connection
		conn.wsConn.Close()
	}()

	conn.wsConn.SetReadLimit(512) // 512 bytes
	conn.wsConn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.wsConn.SetPongHandler(func(string) error {
		conn.wsConn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, message, err := conn.wsConn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket read error: %v", err)
			}
			break
		}

		// Parse message
		var wsMsg WSMessage
		if err := json.Unmarshal(message, &wsMsg); err != nil {
			log.Printf("Error parsing message: %v", err)
			continue
		}

		// Handle message
		h.handleMessage(conn, wsMsg)
	}
}

// writePump writes messages to the WebSocket connection
func (h *WebSocketHandler) writePump(conn *WSConnection) {
	ticker := time.NewTicker(54 * time.Second)
	defer func() {
		ticker.Stop()
		conn.wsConn.Close()
	}()

	for {
		select {
		case message, ok := <-conn.Send:
			conn.wsConn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				conn.wsConn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := conn.wsConn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			conn.wsConn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.wsConn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// handleMessage processes incoming messages
func (h *WebSocketHandler) handleMessage(conn *WSConnection, msg WSMessage) {
	switch msg.Type {
	case "message":
		h.handleChatMessage(conn, msg)
	case "join":
		h.handleJoinRoom(conn, msg)
	case "leave":
		h.handleLeaveRoom(conn, msg)
	case "typing":
		h.handleTyping(conn, msg)
	default:
		log.Printf("Unknown message type: %s", msg.Type)
	}
}

// handleChatMessage handles chat messages
func (h *WebSocketHandler) handleChatMessage(conn *WSConnection, msg WSMessage) {
	// Store message in database
	messageID := uuid.New().String()
	timestamp := time.Now()

	query := `INSERT INTO messages (id, user_id, username, room_id, content, message_type, timestamp, metadata) 
			  VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	
	ctx := context.Background()
	_, err := h.db.ExecContext(ctx, query, 
		messageID, conn.UserID, conn.Username, conn.RoomID, 
		msg.Content, "text", timestamp, msg.Metadata)
	
	if err != nil {
		log.Printf("Error storing message: %v", err)
		return
	}

	// Create message to broadcast
	broadcastMsg := WSMessage{
		Type:      "message",
		UserID:    conn.UserID,
		Username:  conn.Username,
		RoomID:    conn.RoomID,
		Content:   msg.Content,
		MessageID: messageID,
		Timestamp: timestamp.Unix(),
		Metadata:  msg.Metadata,
	}

	// Broadcast to all connections in the room
	h.broadcastToRoom(conn.RoomID, broadcastMsg)

	// Publish to Redis for other instances
	h.publishToRedis(conn.RoomID, broadcastMsg)
}

// handleJoinRoom handles room join requests
func (h *WebSocketHandler) handleJoinRoom(conn *WSConnection, msg WSMessage) {
	// Add user to room in database
	query := `INSERT INTO room_members (room_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`
	ctx := context.Background()
	_, err := h.db.ExecContext(ctx, query, conn.RoomID, conn.UserID)
	
	if err != nil {
		log.Printf("Error joining room: %v", err)
		return
	}

	// Update user status
	updateQuery := `UPDATE users SET status = 'online', last_seen = NOW() WHERE id = $1`
	h.db.ExecContext(ctx, updateQuery, conn.UserID)

	// Send join notification
	joinMsg := WSMessage{
		Type:      "join",
		UserID:    conn.UserID,
		Username:  conn.Username,
		RoomID:    conn.RoomID,
		Content:   fmt.Sprintf("%s joined the room", conn.Username),
		MessageID: uuid.New().String(),
		Timestamp: time.Now().Unix(),
	}

	h.broadcastToRoom(conn.RoomID, joinMsg)
}

// handleLeaveRoom handles room leave requests
func (h *WebSocketHandler) handleLeaveRoom(conn *WSConnection, msg WSMessage) {
	// Remove user from room in database
	query := `DELETE FROM room_members WHERE room_id = $1 AND user_id = $2`
	ctx := context.Background()
	h.db.ExecContext(ctx, query, conn.RoomID, conn.UserID)

	// Send leave notification
	leaveMsg := WSMessage{
		Type:      "leave",
		UserID:    conn.UserID,
		Username:  conn.Username,
		RoomID:    conn.RoomID,
		Content:   fmt.Sprintf("%s left the room", conn.Username),
		MessageID: uuid.New().String(),
		Timestamp: time.Now().Unix(),
	}

	h.broadcastToRoom(conn.RoomID, leaveMsg)
}

// handleTyping handles typing indicators
func (h *WebSocketHandler) handleTyping(conn *WSConnection, msg WSMessage) {
	typingMsg := WSMessage{
		Type:      "typing",
		UserID:    conn.UserID,
		Username:  conn.Username,
		RoomID:    conn.RoomID,
		Content:   msg.Content, // "start" or "stop"
		MessageID: uuid.New().String(),
		Timestamp: time.Now().Unix(),
	}

	h.broadcastToRoom(conn.RoomID, typingMsg)
}

// broadcastToRoom broadcasts a message to all connections in a room
func (h *WebSocketHandler) broadcastToRoom(roomID string, msg WSMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Error marshaling message: %v", err)
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, conn := range h.hub.Connections {
		if conn.RoomID == roomID {
			select {
			case conn.Send <- data:
			default:
				close(conn.Send)
				delete(h.hub.Connections, conn.ID)
			}
		}
	}
}

// publishToRedis publishes a message to Redis
func (h *WebSocketHandler) publishToRedis(roomID string, msg WSMessage) {
	channel := fmt.Sprintf("room:%s", roomID)
	ctx := context.Background()
	
	if err := h.redis.Publish(ctx, channel, msg); err != nil {
		log.Printf("Error publishing to Redis: %v", err)
	}
}

// listenRedisMessages listens for messages from Redis
func (h *WebSocketHandler) listenRedisMessages() {
	ctx := context.Background()
	
	// Subscribe to all room channels
	pubsub := h.redis.Subscribe(ctx, "room:*")
	defer pubsub.Close()

	for {
		msg, err := pubsub.ReceiveMessage(ctx)
		if err != nil {
			log.Printf("Error receiving Redis message: %v", err)
			continue
		}

		// Parse message
		var wsMsg WSMessage
		if err := json.Unmarshal([]byte(msg.Payload), &wsMsg); err != nil {
			log.Printf("Error parsing Redis message: %v", err)
			continue
		}

		// Broadcast to local connections
		h.broadcastToRoom(wsMsg.RoomID, wsMsg)
	}
}

// sendMessage sends a message to a specific connection
func (conn *WSConnection) sendMessage(msg WSMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	conn.wsConn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return conn.wsConn.WriteMessage(websocket.TextMessage, data)
}
