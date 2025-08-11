package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"chat-app/internal/api"
	"chat-app/internal/database"
	"chat-app/internal/grpc"
	"chat-app/internal/redis"
	"chat-app/internal/websocket"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/rs/cors"
)

func main() {
	// Load environment variables
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using default values")
	}

	// Initialize database
	db, err := database.NewConnection()
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Initialize database tables
	if err := db.InitTables(); err != nil {
		log.Fatalf("Failed to initialize database tables: %v", err)
	}

	// Initialize Redis
	redisClient, err := redis.NewRedisClient()
	if err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	defer redisClient.Close()

	// Initialize API handler
	handler := api.NewHandler(db, redisClient)

	// Initialize WebSocket handler
	wsHandler := websocket.NewWebSocketHandler(db, redisClient)

	// Setup Gin router
	router := gin.Default()

	// CORS middleware
	corsMiddleware := cors.New(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"*"},
		AllowCredentials: true,
	})

	// Public routes
	router.POST("/api/auth/register", handler.Register)
	router.POST("/api/auth/login", handler.Login)

	// Protected routes
	protected := router.Group("/api")
	protected.Use(handler.AuthMiddleware())
	{
		protected.GET("/rooms", handler.GetRooms)
		protected.POST("/rooms", handler.CreateRoom)
		protected.GET("/rooms/:roomID/messages", handler.GetMessages)
		protected.POST("/rooms/:roomID/messages", handler.SendMessage)
		protected.GET("/rooms/:roomID/users", handler.GetOnlineUsers)
	}

	// WebSocket endpoint
	router.GET("/ws", wsHandler.HandleWebSocket)

	// Health check
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":    "healthy",
			"timestamp": time.Now().Unix(),
		})
	})

	// Get port from environment or use default
	port := getEnv("HTTP_PORT", "8080")
	grpcPort := getEnv("GRPC_PORT", "50051")

	// Create HTTP server
	httpServer := &http.Server{
		Addr:    ":" + port,
		Handler: corsMiddleware.Handler(router),
	}

	// Start HTTP server in a goroutine
	go func() {
		log.Printf("HTTP server starting on port %s", port)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Start gRPC server in a goroutine
	go func() {
		log.Printf("gRPC server starting on port %s", grpcPort)
		if err := grpc.StartGRPCServer(db, redisClient, grpcPort); err != nil {
			log.Fatalf("gRPC server error: %v", err)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	// Create a deadline for server shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Shutdown HTTP server
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Fatal("HTTP server forced to shutdown:", err)
	}

	log.Println("Server exited")
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
