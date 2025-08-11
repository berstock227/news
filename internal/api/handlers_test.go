package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Mock database and Redis for testing
type MockDB struct {
	mock.Mock
}

type MockRedis struct {
	mock.Mock
}

func TestRegister(t *testing.T) {
	// Set Gin to test mode
	gin.SetMode(gin.TestMode)

	// Create a new router
	router := gin.New()
	
	// Create mock dependencies
	mockDB := &MockDB{}
	mockRedis := &MockRedis{}
	
	// Create handler
	handler := &Handler{
		db:    mockDB,
		redis: mockRedis,
	}

	// Add route
	router.POST("/register", handler.Register)

	// Test cases
	tests := []struct {
		name           string
		payload        map[string]interface{}
		expectedStatus int
		expectedBody   map[string]interface{}
	}{
		{
			name: "Valid registration",
			payload: map[string]interface{}{
				"username": "testuser",
				"email":    "test@example.com",
				"password": "password123",
			},
			expectedStatus: http.StatusCreated,
			expectedBody: map[string]interface{}{
				"message": "User created successfully",
			},
		},
		{
			name: "Invalid email",
			payload: map[string]interface{}{
				"username": "testuser",
				"email":    "invalid-email",
				"password": "password123",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "Missing required fields",
			payload: map[string]interface{}{
				"username": "testuser",
				// Missing email and password
			},
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create request body
			body, _ := json.Marshal(tt.payload)
			req, _ := http.NewRequest("POST", "/register", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")

			// Create response recorder
			w := httptest.NewRecorder()

			// Set up mock expectations
			if tt.expectedStatus == http.StatusCreated {
				mockDB.On("QueryRow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
				mockDB.On("ExecContext", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
			}

			// Perform request
			router.ServeHTTP(w, req)

			// Assertions
			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectedBody != nil {
				var response map[string]interface{}
				err := json.Unmarshal(w.Body.Bytes(), &response)
				assert.NoError(t, err)
				
				for key, expectedValue := range tt.expectedBody {
					assert.Equal(t, expectedValue, response[key])
				}
			}
		})
	}
}

func TestLogin(t *testing.T) {
	// Set Gin to test mode
	gin.SetMode(gin.TestMode)

	// Create a new router
	router := gin.New()
	
	// Create mock dependencies
	mockDB := &MockDB{}
	mockRedis := &MockRedis{}
	
	// Create handler
	handler := &Handler{
		db:    mockDB,
		redis: mockRedis,
	}

	// Add route
	router.POST("/login", handler.Login)

	// Test cases
	tests := []struct {
		name           string
		payload        map[string]interface{}
		expectedStatus int
		expectedBody   map[string]interface{}
	}{
		{
			name: "Valid login",
			payload: map[string]interface{}{
				"email":    "test@example.com",
				"password": "password123",
			},
			expectedStatus: http.StatusOK,
			expectedBody: map[string]interface{}{
				"message": "Login successful",
			},
		},
		{
			name: "Invalid email format",
			payload: map[string]interface{}{
				"email":    "invalid-email",
				"password": "password123",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "Missing password",
			payload: map[string]interface{}{
				"email": "test@example.com",
				// Missing password
			},
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create request body
			body, _ := json.Marshal(tt.payload)
			req, _ := http.NewRequest("POST", "/login", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")

			// Create response recorder
			w := httptest.NewRecorder()

			// Set up mock expectations for valid login
			if tt.expectedStatus == http.StatusOK {
				// Mock database query for user lookup
				mockDB.On("QueryRowContext", mock.Anything, mock.Anything, mock.Anything).Return(nil)
				// Mock password update
				mockDB.On("ExecContext", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
			}

			// Perform request
			router.ServeHTTP(w, req)

			// Assertions
			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectedBody != nil {
				var response map[string]interface{}
				err := json.Unmarshal(w.Body.Bytes(), &response)
				assert.NoError(t, err)
				
				for key, expectedValue := range tt.expectedBody {
					assert.Equal(t, expectedValue, response[key])
				}
			}
		})
	}
}

func TestAuthMiddleware(t *testing.T) {
	// Set Gin to test mode
	gin.SetMode(gin.TestMode)

	// Create a new router
	router := gin.New()
	
	// Create mock dependencies
	mockDB := &MockDB{}
	mockRedis := &MockRedis{}
	
	// Create handler
	handler := &Handler{
		db:    mockDB,
		redis: mockRedis,
	}

	// Add protected route
	protected := router.Group("/api")
	protected.Use(handler.AuthMiddleware())
	{
		protected.GET("/test", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"message": "protected"})
		})
	}

	// Test cases
	tests := []struct {
		name           string
		authHeader     string
		expectedStatus int
	}{
		{
			name:           "No authorization header",
			authHeader:     "",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Invalid token format",
			authHeader:     "InvalidToken",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Invalid JWT token",
			authHeader:     "Bearer invalid.token.here",
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create request
			req, _ := http.NewRequest("GET", "/api/test", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			// Create response recorder
			w := httptest.NewRecorder()

			// Perform request
			router.ServeHTTP(w, req)

			// Assertions
			assert.Equal(t, tt.expectedStatus, w.Code)
		})
	}
}

// Benchmark tests
func BenchmarkRegister(b *testing.B) {
	// Set Gin to test mode
	gin.SetMode(gin.TestMode)

	// Create a new router
	router := gin.New()
	
	// Create mock dependencies
	mockDB := &MockDB{}
	mockRedis := &MockRedis{}
	
	// Create handler
	handler := &Handler{
		db:    mockDB,
		redis: mockRedis,
	}

	// Add route
	router.POST("/register", handler.Register)

	// Create request body
	payload := map[string]interface{}{
		"username": "benchuser",
		"email":    "bench@example.com",
		"password": "password123",
	}
	body, _ := json.Marshal(payload)

	// Set up mock expectations
	mockDB.On("QueryRow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockDB.On("ExecContext", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		req, _ := http.NewRequest("POST", "/register", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
	}
}

func BenchmarkLogin(b *testing.B) {
	// Set Gin to test mode
	gin.SetMode(gin.TestMode)

	// Create a new router
	router := gin.New()
	
	// Create mock dependencies
	mockDB := &MockDB{}
	mockRedis := &MockRedis{}
	
	// Create handler
	handler := &Handler{
		db:    mockDB,
		redis: mockRedis,
	}

	// Add route
	router.POST("/login", handler.Login)

	// Create request body
	payload := map[string]interface{}{
		"email":    "bench@example.com",
		"password": "password123",
	}
	body, _ := json.Marshal(payload)

	// Set up mock expectations
	mockDB.On("QueryRowContext", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockDB.On("ExecContext", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		req, _ := http.NewRequest("POST", "/login", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
	}
}
