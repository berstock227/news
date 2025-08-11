#!/bin/bash

echo "Testing Docker build..."

# Build the Docker image
docker build -t chat-app:test .

# Check if build was successful
if [ $? -eq 0 ]; then
    echo "✅ Docker build successful!"
    
    # Test running the container
    echo "Testing container startup..."
    docker run --rm -d --name chat-app-test -p 8080:8080 -p 50051:50051 chat-app:test
    
    # Wait a moment for the container to start
    sleep 5
    
    # Check if container is running
    if docker ps | grep -q chat-app-test; then
        echo "✅ Container started successfully!"
        
        # Test health endpoint
        echo "Testing health endpoint..."
        if curl -f http://localhost:8080/health > /dev/null 2>&1; then
            echo "✅ Health endpoint is working!"
        else
            echo "❌ Health endpoint failed"
        fi
        
        # Stop the test container
        docker stop chat-app-test
    else
        echo "❌ Container failed to start"
    fi
else
    echo "❌ Docker build failed!"
    exit 1
fi
