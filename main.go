package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

// Constants for board dimensions.
const (
	numRows = 11
	numCols = 64
)

// --- WebSocket Upgrade Configuration ---
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// In production, restrict allowed origins.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Message is used for JSON message parsing.
type Message struct {
	Type  string `json:"type"`
	Row   int    `json:"row,omitempty"`
	Col   int    `json:"col,omitempty"`
	Value bool   `json:"value,omitempty"`
}

// InboundMessage wraps an incoming message along with the client that sent it.
type InboundMessage struct {
	client *Client
	data   []byte
}

// --- Client Structure ---
type Client struct {
	conn *websocket.Conn
	send chan []byte
}

// --- Hub Structure ---
type Hub struct {
	clients    map[*Client]bool
	inbound    chan InboundMessage
	register   chan *Client
	unregister chan *Client
	boardState [][]bool
}

// newHub initializes a new hub and creates an empty board state.
func newHub() *Hub {
	h := &Hub{
		clients:    make(map[*Client]bool),
		inbound:    make(chan InboundMessage),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		boardState: make([][]bool, numRows),
	}
	for i := 0; i < numRows; i++ {
		h.boardState[i] = make([]bool, numCols)
	}
	return h
}

// run listens for register, unregister, and inbound message events.
func (h *Hub) run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client] = true
			// Immediately send the current board state to the new client.
			initMsg := struct {
				Type      string     `json:"type"`
				GridState [][]bool   `json:"gridState"`
			}{
				Type:      "init",
				GridState: h.boardState,
			}
			if data, err := json.Marshal(initMsg); err != nil {
				log.Println("Error marshaling init message:", err)
			} else {
				client.send <- data
			}

		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}

		case im := <-h.inbound:
			var msg Message
			if err := json.Unmarshal(im.data, &msg); err != nil {
				log.Println("Error decoding message:", err)
				continue
			}

			switch msg.Type {
			case "toggle":
				// Update board state.
				if msg.Row >= 0 && msg.Row < numRows && msg.Col >= 0 && msg.Col < numCols {
					h.boardState[msg.Row][msg.Col] = msg.Value
				}
				// Broadcast the toggle message to all clients.
				for client := range h.clients {
					select {
					case client.send <- im.data:
					default:
						close(client.send)
						delete(h.clients, client)
					}
				}

			case "get_state":
				// Send the current board state only to the requesting client.
				initMsg := struct {
					Type      string     `json:"type"`
					GridState [][]bool   `json:"gridState"`
				}{
					Type:      "init",
					GridState: h.boardState,
				}
				if data, err := json.Marshal(initMsg); err != nil {
					log.Println("Error marshaling init message:", err)
				} else {
					im.client.send <- data
				}

			default:
				// For other message types, broadcast them.
				for client := range h.clients {
					select {
					case client.send <- im.data:
					default:
						close(client.send)
						delete(h.clients, client)
					}
				}
			}
		}
	}
}

// readPump reads messages from the client and sends them to hub.inbound.
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
		hub.inbound <- InboundMessage{client: c, data: message}
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

