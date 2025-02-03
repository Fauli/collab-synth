package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

// --- WebSocket Upgrade Configuration ---
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// In a production app, you’ll want to restrict allowed origins
	CheckOrigin: func(r *http.Request) bool { return true },
}

// --- Client Structure ---
type Client struct {
	conn *websocket.Conn
	send chan []byte
}

// --- Hub Structure ---
type Hub struct {
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
}

// newHub initializes a new hub.
func newHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan []byte),
		register:   make(chan *Client),
		unregister: make(chan *Client),
	}
}

// run listens for register, unregister, and broadcast events.
func (h *Hub) run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client] = true

		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}

		case message := <-h.broadcast:
			for client := range h.clients {
				select {
				case client.send <- message:
					// Message queued successfully.
				default:
					// If sending fails (e.g., the channel is blocked), remove the client.
					close(client.send)
					delete(h.clients, client)
				}
			}
		}
	}
}

// readPump reads messages from the client and forwards them to the hub.
func (c *Client) readPump(hub *Hub) {
	defer func() {
		hub.unregister <- c
		c.conn.Close()
	}()
	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
		// Broadcast the received message to all clients.
		hub.broadcast <- message
	}
}

// writePump writes messages from the hub to the client.
func (c *Client) writePump() {
	defer c.conn.Close()
	for message := range c.send {
		if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
			break
		}
	}
}

// serveWs upgrades the HTTP connection to a WebSocket and registers the client.
func serveWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("WebSocket upgrade error:", err)
		return
	}
	client := &Client{conn: conn, send: make(chan []byte, 256)}
	hub.register <- client

	// Start goroutines for reading from and writing to the client.
	go client.writePump()
	client.readPump(hub)
}

func main() {
	hub := newHub()
	go hub.run()

	// WebSocket endpoint.
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		serveWs(hub, w, r)
	})

	// Serve static files (your frontend) from the "static" directory.
	http.Handle("/", http.FileServer(http.Dir("./static")))

	fmt.Println("Server started on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

