package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
)

// Load environment variables
func init() {
	err := godotenv.Load(".env")
	if err != nil {
		log.Fatalf("Error loading .env file: %v", err)
	}
}

// Client struct to hold both TCP and WebSocket connections, and their nickname
type Client struct {
	Conn    net.Conn
	WSConn  *websocket.Conn
	Name    string
	Address string
}

// ChatServer struct to manage all connected clients
type ChatServer struct {
	Clients     []*Client
	Mutex       sync.Mutex
	BroadcastCh chan string
}

// Initializes a new chat server
func NewChatServer() *ChatServer {
	return &ChatServer{
		Clients:     make([]*Client, 0),
		BroadcastCh: make(chan string),
	}
}

// AddClient adds a new client to the server
func (cs *ChatServer) AddClient(client *Client) {
	cs.Mutex.Lock()
	defer cs.Mutex.Unlock()
	cs.Clients = append(cs.Clients, client)
}

// RemoveClient removes a client from the server
func (cs *ChatServer) RemoveClient(client *Client) {
	cs.Mutex.Lock()
	defer cs.Mutex.Unlock()
	for i, c := range cs.Clients {
		if c == client {
			cs.Clients = append(cs.Clients[:i], cs.Clients[i+1:]...)
			break
		}
	}
}

// Broadcast sends a message to all clients
func (cs *ChatServer) Broadcast(msg string, sender interface{}) {
	cs.Mutex.Lock()
	defer cs.Mutex.Unlock()

	for _, client := range cs.Clients {
		// Skip sending the message back to the sender
		if (client.Conn != nil && client.Conn == sender) || (client.WSConn != nil && client.WSConn == sender) {
			continue
		}

		// Send message to TCP clients
		if client.Conn != nil {
			_, err := client.Conn.Write([]byte(msg + "\n"))
			if err != nil {
				log.Println("Broadcast to TCP error:", err)
				client.Conn.Close()
				cs.RemoveClient(client)
			}
		}

		// Send message to WebSocket clients
		if client.WSConn != nil {
			err := client.WSConn.WriteMessage(websocket.TextMessage, []byte(msg))
			if err != nil {
				log.Println("Broadcast to WebSocket error:", err)
				client.WSConn.Close()
				cs.RemoveClient(client)
			}
		}
	}
}

// DisplayClients constantly refreshes the list of connected clients in a table format
func (cs *ChatServer) DisplayClients() {
	//var msg string
	for {
		cs.Mutex.Lock()

		// Clear the screen using ANSI escape code
		fmt.Print("\033[H\033[2J")
		fmt.Println("Connected clients:")
		fmt.Println("----------------------------------------------------------------")
		fmt.Printf("| %-15s | %-25s | %-15s |\n", "Type", "Address", "Nickname")
		fmt.Println("----------------------------------------------------------------")

		// Print each connected client in a table format
		for _, client := range cs.Clients {
			if client.Conn != nil {
				fmt.Printf("| %-15s | %-25s | %-15s |\n", "TCP Client", client.Address, client.Name)
			}
			if client.WSConn != nil {
				fmt.Printf("| %-15s | %-25s | %-15s |\n", "WebSocket Client", client.Address, client.Name)
			}
		}
		fmt.Println("----------------------------------------------------------------")

		cs.Mutex.Unlock()

		time.Sleep(2 * time.Second) // Refresh every 2 seconds
	}
}

// HandleTCPConnection handles new TCP clients
func (cs *ChatServer) HandleTCPConnection(conn net.Conn) {
	address := conn.RemoteAddr().String()
	client := &Client{Conn: conn, Address: address}
	cs.AddClient(client)
	defer conn.Close()
	defer cs.RemoveClient(client)

	// Ask for a nickname
	conn.Write([]byte("Please enter your nickname: "))
	nickBuf := make([]byte, 1024)
	n, err := conn.Read(nickBuf)
	if err != nil {
		return
	}
	client.Name = strings.TrimSpace(string(nickBuf[:n]))

	// Notify all other clients
	cs.Broadcast(fmt.Sprintf("%s has joined the chat!", client.Name), conn)

	buf := make([]byte, 1024)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			cs.Broadcast(fmt.Sprintf("%s has left the chat.", client.Name), conn)
			return
		}
		msg := fmt.Sprintf("%s: %s", client.Name, string(buf[:n]))
		cs.Broadcast(msg, conn)
	}
}

// HandleWebSocketConnection handles new WebSocket clients
func (cs *ChatServer) HandleWebSocketConnection(wsConn *websocket.Conn) {
	address := wsConn.RemoteAddr().String()
	client := &Client{WSConn: wsConn, Address: address}
	cs.AddClient(client)

	defer wsConn.Close()
	defer cs.RemoveClient(client)

	// Ask for login or registration
	wsConn.WriteMessage(websocket.TextMessage, []byte("1. Login\n2. Register"))
	_, response, err := wsConn.ReadMessage()
	if err != nil {
		return
	}

	str := string(response)
	res, err := strconv.Atoi(str)
	if err != nil {
		return
	}
	// Ask for username
	wsConn.WriteMessage(websocket.TextMessage, []byte("Please enter username:"))
	_, username, err := wsConn.ReadMessage()
	if err != nil {
		return
	}

	// Ask for password
	wsConn.WriteMessage(websocket.TextMessage, []byte("Please enter password:"))
	_, password, err := wsConn.ReadMessage()
	if err != nil {
		return
	}
	url := os.Getenv("AUTH_URL")
	data := LoginRequest{
		Username: string(username),
		Password: string(password),
	}
	loginDataJSON, err := json.Marshal(data)
	if err != nil {
		log.Fatalf("Error marshalling login data: %v", err)
	}
	if res == 1 {
		resp, err := http.Post(url+"/login", "application/json", bytes.NewBuffer(loginDataJSON))
		if err != nil {
			log.Fatalf("Error making POST request: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			log.Fatalf("Login failed with status code: %d", resp.StatusCode)
		}
		// Decode the response body
		var loginResponse LoginResponse
		err = json.NewDecoder(resp.Body).Decode(&loginResponse)
		if err != nil {
			log.Fatalf("Error decoding response: %v", err)
		}

		// Print the received token (if the login is successful)
		message := fmt.Sprintf("%s logged in successfully", username)
		wsConn.WriteMessage(websocket.TextMessage, []byte(message))
		fmt.Printf("Login successful, received token: %s\n", loginResponse.Token)
	} else if res == 2 {
		resp, err := http.Post(url+"/register", "application/json", bytes.NewBuffer(loginDataJSON))
		if err != nil {
			log.Fatalf("Error making POST request: %v\n", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			log.Fatalf("Login failed with status code: %d", resp.StatusCode)
		}
		message := fmt.Sprintf("%s created successfully", username)
		wsConn.WriteMessage(websocket.TextMessage, []byte(message))
	}

	client.Name = strings.TrimSpace(string(username))
	// Notify other clients
	cs.Broadcast(fmt.Sprintf("%s has joined the chat!", client.Name), wsConn)

	for {
		_, msg, err := wsConn.ReadMessage()
		if err != nil {
			cs.Broadcast(fmt.Sprintf("%s has left the chat.", client.Name), wsConn)
			return
		}
		cs.Broadcast(fmt.Sprintf("%s: %s", client.Name, string(msg)), wsConn)
	}
}

// Starts the WebSocket server
func (cs *ChatServer) StartWebSocketServer() {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		wsConn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println("WebSocket upgrade error:", err)
			return
		}
		cs.HandleWebSocketConnection(wsConn)
	})

	log.Println("WebSocket server listening on :8081")
	log.Fatal(http.ListenAndServe("0.0.0.0:8081", nil))
}

// Starts the TCP chat server
func (cs *ChatServer) StartTCPServer() {
	listener, err := net.Listen("tcp", ":8080")
	if err != nil {
		log.Fatal("TCP Server error:", err)
	}
	defer listener.Close()

	log.Println("TCP server listening on :8080")
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Println("TCP connection error:", err)
			continue
		}
		go cs.HandleTCPConnection(conn)
	}
}

func main() {
	chatServer := NewChatServer()

	// Start a goroutine to constantly display connected clients in table format
	go chatServer.DisplayClients()

	// Start TCP and WebSocket servers
	go chatServer.StartTCPServer()
	go chatServer.StartWebSocketServer()

	select {}
}
