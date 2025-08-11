import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate } from 'k6/metrics';

// Custom metrics
const errorRate = new Rate('errors');

// Test configuration
export const options = {
  stages: [
    { duration: '2m', target: 100 }, // Ramp up to 100 users
    { duration: '5m', target: 100 }, // Stay at 100 users
    { duration: '2m', target: 200 }, // Ramp up to 200 users
    { duration: '5m', target: 200 }, // Stay at 200 users
    { duration: '2m', target: 0 },   // Ramp down to 0 users
  ],
  thresholds: {
    http_req_duration: ['p(95)<500'], // 95% of requests must complete below 500ms
    http_req_failed: ['rate<0.1'],    // Error rate must be below 10%
    errors: ['rate<0.1'],             // Custom error rate must be below 10%
  },
};

// Test data
const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const USERS = [];

// Setup function to create test users
export function setup() {
  console.log('Setting up test users...');
  
  for (let i = 0; i < 50; i++) {
    const userData = {
      username: `testuser${i}`,
      email: `testuser${i}@example.com`,
      password: 'password123',
    };

    const registerRes = http.post(`${BASE_URL}/api/auth/register`, JSON.stringify(userData), {
      headers: { 'Content-Type': 'application/json' },
    });

    if (registerRes.status === 201) {
      const response = JSON.parse(registerRes.body);
      USERS.push({
        ...userData,
        token: response.token,
        id: response.user.id,
      });
    }
  }

  console.log(`Created ${USERS.length} test users`);
  return { users: USERS };
}

// Main test function
export default function(data) {
  const user = data.users[Math.floor(Math.random() * data.users.length)];
  
  if (!user) {
    console.log('No users available for testing');
    return;
  }

  const headers = {
    'Content-Type': 'application/json',
    'Authorization': `Bearer ${user.token}`,
  };

  // Test 1: Get rooms
  const roomsRes = http.get(`${BASE_URL}/api/rooms`, { headers });
  check(roomsRes, {
    'rooms status is 200': (r) => r.status === 200,
    'rooms response time < 200ms': (r) => r.timings.duration < 200,
  }) || errorRate.add(1);

  sleep(1);

  // Test 2: Create a room (occasionally)
  if (Math.random() < 0.1) { // 10% chance
    const roomData = {
      name: `Test Room ${Date.now()}`,
      description: 'Load test room',
      is_private: false,
    };

    const createRoomRes = http.post(`${BASE_URL}/api/rooms`, JSON.stringify(roomData), { headers });
    check(createRoomRes, {
      'create room status is 201': (r) => r.status === 201,
      'create room response time < 500ms': (r) => r.timings.duration < 500,
    }) || errorRate.add(1);

    sleep(1);
  }

  // Test 3: Get messages from a room
  const roomId = 'test-room-id'; // In a real test, you'd get this from room creation
  const messagesRes = http.get(`${BASE_URL}/api/rooms/${roomId}/messages?limit=20`, { headers });
  check(messagesRes, {
    'messages status is 200': (r) => r.status === 200,
    'messages response time < 300ms': (r) => r.timings.duration < 300,
  }) || errorRate.add(1);

  sleep(1);

  // Test 4: Send a message (occasionally)
  if (Math.random() < 0.2) { // 20% chance
    const messageData = {
      content: `Load test message ${Date.now()}`,
      room_id: roomId,
      message_type: 'text',
    };

    const sendMessageRes = http.post(`${BASE_URL}/api/rooms/${roomId}/messages`, JSON.stringify(messageData), { headers });
    check(sendMessageRes, {
      'send message status is 201': (r) => r.status === 201,
      'send message response time < 400ms': (r) => r.timings.duration < 400,
    }) || errorRate.add(1);

    sleep(1);
  }

  // Test 5: Get online users
  const usersRes = http.get(`${BASE_URL}/api/rooms/${roomId}/users`, { headers });
  check(usersRes, {
    'users status is 200': (r) => r.status === 200,
    'users response time < 200ms': (r) => r.timings.duration < 200,
  }) || errorRate.add(1);

  sleep(1);
}

// WebSocket load test (separate function)
export function websocketTest() {
  const user = USERS[Math.floor(Math.random() * USERS.length)];
  
  if (!user) return;

  // Note: k6 doesn't have built-in WebSocket support
  // This would require using k6 extensions or a different tool
  // For now, we'll simulate WebSocket-like behavior with HTTP polling
  
  const headers = {
    'Authorization': `Bearer ${user.token}`,
  };

  // Simulate WebSocket connection by polling for messages
  const pollRes = http.get(`${BASE_URL}/api/rooms/test-room-id/messages?limit=1`, { headers });
  check(pollRes, {
    'websocket poll status is 200': (r) => r.status === 200,
    'websocket poll response time < 100ms': (r) => r.timings.duration < 100,
  }) || errorRate.add(1);
}

// Teardown function
export function teardown(data) {
  console.log('Cleaning up test data...');
  // In a real scenario, you might want to clean up test data
}

// Handle setup data
export function handleSummary(data) {
  return {
    'load-test-results.json': JSON.stringify(data),
    stdout: textSummary(data, { indent: ' ', enableColors: true }),
  };
}

// Custom text summary function
function textSummary(data, options) {
  const { metrics, root_group } = data;
  const { http_req_duration, http_req_failed, http_reqs } = metrics;
  
  return `
Load Test Results
=================

HTTP Requests:
  - Total: ${http_reqs.count}
  - Failed: ${http_req_failed.rate * 100}%
  - Average Duration: ${http_req_duration.avg.toFixed(2)}ms
  - 95th Percentile: ${http_req_duration.values['p(95)'].toFixed(2)}ms

Test Stages:
  - Ramp up to 100 users: 2 minutes
  - Stay at 100 users: 5 minutes
  - Ramp up to 200 users: 2 minutes
  - Stay at 200 users: 5 minutes
  - Ramp down: 2 minutes

Thresholds:
  - 95% of requests < 500ms: ${http_req_duration.values['p(95)'] < 500 ? 'PASS' : 'FAIL'}
  - Error rate < 10%: ${http_req_failed.rate < 0.1 ? 'PASS' : 'FAIL'}
`;
}
