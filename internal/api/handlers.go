package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"chat-app/internal/database"
	"chat-app/internal/models"
	"chat-app/internal/redis"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type Handler struct {
	db    *database.DB
	redis *redis.RedisClient
}

type UserRequest struct {
	Username string `json:"username" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
}

type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type MessageRequest struct {
	Content   string            `json:"content" binding:"required"`
	RoomID    string            `json:"room_id" binding:"required"`
	MessageType string          `json:"message_type"`
	Metadata  map[string]string `json:"metadata"`
}

type RoomRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	IsPrivate   bool   `json:"is_private"`
}

// NewHandler creates a new API handler
func NewHandler(db *database.DB, redis *redis.RedisClient) *Handler {
	return &Handler{
		db:    db,
		redis: redis,
	}
}

// Register handles user registration
func (h *Handler) Register(c *gin.Context) {
	var req UserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check if user already exists
	var existingUser models.User
	err := h.db.QueryRow("SELECT id FROM users WHERE email = $1 OR username = $2", 
		req.Email, req.Username).Scan(&existingUser.ID)
	
	if err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "User already exists"})
		return
	}

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	// Create user
	userID := uuid.New().String()
	query := `INSERT INTO users (id, username, email, password, status, created_at, updated_at) 
			  VALUES ($1, $2, $3, $4, 'offline', NOW(), NOW())`
	
	_, err = h.db.ExecContext(c.Request.Context(), query, 
		userID, req.Username, req.Email, string(hashedPassword))
	
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user"})
		return
	}

	// Generate JWT token
	token, err := h.generateJWT(userID, req.Username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "User created successfully",
		"token":   token,
		"user": gin.H{
			"id":       userID,
			"username": req.Username,
			"email":    req.Email,
		},
	})
}

// Login handles user login
func (h *Handler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get user from database
	var user models.User
	query := `SELECT id, username, email, password FROM users WHERE email = $1`
	err := h.db.QueryRowContext(c.Request.Context(), query, req.Email).Scan(
		&user.ID, &user.Username, &user.Email, &user.Password)
	
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	}

	// Check password
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	}

	// Update user status to online
	updateQuery := `UPDATE users SET status = 'online', last_seen = NOW() WHERE id = $1`
	h.db.ExecContext(c.Request.Context(), updateQuery, user.ID)

	// Generate JWT token
	token, err := h.generateJWT(user.ID, user.Username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Login successful",
		"token":   token,
		"user": gin.H{
			"id":       user.ID,
			"username": user.Username,
			"email":    user.Email,
		},
	})
}

// GetRooms gets all rooms
func (h *Handler) GetRooms(c *gin.Context) {
	userID := c.GetString("user_id")
	
	query := `SELECT r.id, r.name, r.description, r.is_private, r.created_by, r.created_at, r.updated_at
			  FROM rooms r
			  LEFT JOIN room_members rm ON r.id = rm.room_id AND rm.user_id = $1
			  WHERE r.is_private = false OR rm.user_id = $1
			  ORDER BY r.created_at DESC`
	
	rows, err := h.db.QueryContext(c.Request.Context(), query, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get rooms"})
		return
	}
	defer rows.Close()

	var rooms []models.Room
	for rows.Next() {
		var room models.Room
		err := rows.Scan(&room.ID, &room.Name, &room.Description, &room.IsPrivate, 
			&room.CreatedBy, &room.CreatedAt, &room.UpdatedAt)
		if err != nil {
			continue
		}
		rooms = append(rooms, room)
	}

	c.JSON(http.StatusOK, gin.H{"rooms": rooms})
}

// CreateRoom creates a new room
func (h *Handler) CreateRoom(c *gin.Context) {
	var req RoomRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID := c.GetString("user_id")
	roomID := uuid.New().String()

	query := `INSERT INTO rooms (id, name, description, is_private, created_by, created_at, updated_at) 
			  VALUES ($1, $2, $3, $4, $5, NOW(), NOW())`
	
	_, err := h.db.ExecContext(c.Request.Context(), query, 
		roomID, req.Name, req.Description, req.IsPrivate, userID)
	
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create room"})
		return
	}

	// Add creator to room members
	memberQuery := `INSERT INTO room_members (room_id, user_id) VALUES ($1, $2)`
	h.db.ExecContext(c.Request.Context(), memberQuery, roomID, userID)

	c.JSON(http.StatusCreated, gin.H{
		"message": "Room created successfully",
		"room": gin.H{
			"id":          roomID,
			"name":        req.Name,
			"description": req.Description,
			"is_private":  req.IsPrivate,
			"created_by":  userID,
		},
	})
}

// GetMessages gets messages for a room
func (h *Handler) GetMessages(c *gin.Context) {
	roomID := c.Param("roomID")
	limitStr := c.DefaultQuery("limit", "50")
	beforeStr := c.Query("before")

	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 || limit > 100 {
		limit = 50
	}

	query := `SELECT m.id, m.user_id, m.username, m.room_id, m.content, m.message_type, m.timestamp, m.metadata
			  FROM messages m
			  WHERE m.room_id = $1
			  ORDER BY m.timestamp DESC
			  LIMIT $2`

	var rows *sql.Rows
	if beforeStr != "" {
		before, err := strconv.ParseInt(beforeStr, 10, 64)
		if err == nil {
			query = `SELECT m.id, m.user_id, m.username, m.room_id, m.content, m.message_type, m.timestamp, m.metadata
					 FROM messages m
					 WHERE m.room_id = $1 AND m.timestamp < $2
					 ORDER BY m.timestamp DESC
					 LIMIT $3`
			rows, err = h.db.QueryContext(c.Request.Context(), query, roomID, time.Unix(before, 0), limit)
		}
	} else {
		rows, err = h.db.QueryContext(c.Request.Context(), query, roomID, limit)
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get messages"})
		return
	}
	defer rows.Close()

	var messages []models.Message
	for rows.Next() {
		var msg models.Message
		var metadataJSON []byte
		
		err := rows.Scan(&msg.ID, &msg.UserID, &msg.Username, &msg.RoomID, 
			&msg.Content, &msg.MessageType, &msg.Timestamp, &metadataJSON)
		if err != nil {
			continue
		}

		// Parse metadata if needed
		if len(metadataJSON) > 0 {
			msg.Metadata = make(map[string]string)
		}

		messages = append(messages, msg)
	}

	c.JSON(http.StatusOK, gin.H{
		"messages": messages,
		"has_more": len(messages) == limit,
	})
}

// SendMessage sends a message
func (h *Handler) SendMessage(c *gin.Context) {
	var req MessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID := c.GetString("user_id")
	username := c.GetString("username")
	messageID := uuid.New().String()
	timestamp := time.Now()

	// Store message in database
	query := `INSERT INTO messages (id, user_id, username, room_id, content, message_type, timestamp, metadata) 
			  VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	
	_, err := h.db.ExecContext(c.Request.Context(), query, 
		messageID, userID, username, req.RoomID, 
		req.Content, req.MessageType, timestamp, req.Metadata)
	
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to send message"})
		return
	}

	// Publish to Redis for real-time delivery
	messageData := map[string]interface{}{
		"id":           messageID,
		"user_id":      userID,
		"username":     username,
		"room_id":      req.RoomID,
		"content":      req.Content,
		"message_type": req.MessageType,
		"timestamp":    timestamp.Unix(),
		"metadata":     req.Metadata,
	}

	channel := fmt.Sprintf("room:%s", req.RoomID)
	h.redis.Publish(c.Request.Context(), channel, messageData)

	c.JSON(http.StatusCreated, gin.H{
		"message": "Message sent successfully",
		"message_id": messageID,
	})
}

// GetOnlineUsers gets online users in a room
func (h *Handler) GetOnlineUsers(c *gin.Context) {
	roomID := c.Param("roomID")

	// Try Redis first
	roomKey := fmt.Sprintf("room:%s:users", roomID)
	userIDs, err := h.redis.SMembers(c.Request.Context(), roomKey)
	
	if err != nil {
		// Fallback to database
		query := `SELECT u.id, u.username, u.status, u.last_seen 
				  FROM users u 
				  JOIN room_members rm ON u.id = rm.user_id 
				  WHERE rm.room_id = $1 AND u.status = 'online'`
		
		rows, err := h.db.QueryContext(c.Request.Context(), query, roomID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get online users"})
			return
		}
		defer rows.Close()

		var users []models.User
		for rows.Next() {
			var user models.User
			err := rows.Scan(&user.ID, &user.Username, &user.Status, &user.LastSeen)
			if err != nil {
				continue
			}
			users = append(users, user)
		}

		c.JSON(http.StatusOK, gin.H{"users": users})
		return
	}

	if len(userIDs) == 0 {
		c.JSON(http.StatusOK, gin.H{"users": []models.User{}})
		return
	}

	// Get user details from database
	query := `SELECT id, username, status, last_seen FROM users WHERE id = ANY($1)`
	rows, err := h.db.QueryContext(c.Request.Context(), query, userIDs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user details"})
		return
	}
	defer rows.Close()

	var users []models.User
	for rows.Next() {
		var user models.User
		err := rows.Scan(&user.ID, &user.Username, &user.Status, &user.LastSeen)
		if err != nil {
			continue
		}
		users = append(users, user)
	}

	c.JSON(http.StatusOK, gin.H{"users": users})
}

// generateJWT generates a JWT token
func (h *Handler) generateJWT(userID, username string) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id":  userID,
		"username": username,
		"exp":      time.Now().Add(time.Hour * 24).Unix(),
	})

	// In production, use environment variable for secret
	return token.SignedString([]byte("your-secret-key"))
}

// AuthMiddleware validates JWT tokens
func (h *Handler) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenString := c.GetHeader("Authorization")
		if tokenString == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization header required"})
			c.Abort()
			return
		}

		// Remove "Bearer " prefix
		if len(tokenString) > 7 && tokenString[:7] == "Bearer " {
			tokenString = tokenString[7:]
		}

		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			return []byte("your-secret-key"), nil
		})

		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
			c.Abort()
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token claims"})
			c.Abort()
			return
		}

		userID, ok := claims["user_id"].(string)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid user ID"})
			c.Abort()
			return
		}

		username, ok := claims["username"].(string)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid username"})
			c.Abort()
			return
		}

		c.Set("user_id", userID)
		c.Set("username", username)
		c.Next()
	}
}
