package models

import (
	"time"
	"github.com/google/uuid"
)

// User represents a chat user
type User struct {
	ID        string    `json:"id" db:"id"`
	Username  string    `json:"username" db:"username"`
	Email     string    `json:"email" db:"email"`
	Password  string    `json:"-" db:"password"`
	Status    string    `json:"status" db:"status"` // online, offline, away
	LastSeen  time.Time `json:"last_seen" db:"last_seen"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// Message represents a chat message
type Message struct {
	ID          string            `json:"id" db:"id"`
	UserID      string            `json:"user_id" db:"user_id"`
	Username    string            `json:"username" db:"username"`
	RoomID      string            `json:"room_id" db:"room_id"`
	Content     string            `json:"content" db:"content"`
	MessageType string            `json:"message_type" db:"message_type"` // text, image, file
	Timestamp   time.Time         `json:"timestamp" db:"timestamp"`
	Metadata    map[string]string `json:"metadata" db:"metadata"`
	CreatedAt   time.Time         `json:"created_at" db:"created_at"`
}

// Room represents a chat room
type Room struct {
	ID          string    `json:"id" db:"id"`
	Name        string    `json:"name" db:"name"`
	Description string    `json:"description" db:"description"`
	IsPrivate   bool      `json:"is_private" db:"is_private"`
	CreatedBy   string    `json:"created_by" db:"created_by"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// Connection represents a WebSocket connection
type Connection struct {
	ID       string          `json:"id"`
	UserID   string          `json:"user_id"`
	Username string          `json:"username"`
	RoomID   string          `json:"room_id"`
	Conn     interface{}     `json:"-"` // WebSocket connection
	Send     chan []byte     `json:"-"`
	Hub      *Hub            `json:"-"`
}

// Hub manages all WebSocket connections
type Hub struct {
	Connections map[string]*Connection
	Broadcast   chan []byte
	Register    chan *Connection
	Unregister  chan *Connection
}

// NewConnection creates a new connection
func NewConnection(userID, username, roomID string, conn interface{}, hub *Hub) *Connection {
	return &Connection{
		ID:       uuid.New().String(),
		UserID:   userID,
		Username: username,
		RoomID:   roomID,
		Conn:     conn,
		Send:     make(chan []byte, 256),
		Hub:      hub,
	}
}

// NewHub creates a new hub
func NewHub() *Hub {
	return &Hub{
		Connections: make(map[string]*Connection),
		Broadcast:   make(chan []byte),
		Register:    make(chan *Connection),
		Unregister:  make(chan *Connection),
	}
}

// Run starts the hub
func (h *Hub) Run() {
	for {
		select {
		case conn := <-h.Register:
			h.Connections[conn.ID] = conn
		case conn := <-h.Unregister:
			if _, ok := h.Connections[conn.ID]; ok {
				delete(h.Connections, conn.ID)
				close(conn.Send)
			}
		case message := <-h.Broadcast:
			for _, conn := range h.Connections {
				select {
				case conn.Send <- message:
				default:
					close(conn.Send)
					delete(h.Connections, conn.ID)
				}
			}
		}
	}
}
