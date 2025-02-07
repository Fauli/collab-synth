package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"

	"github.com/gorilla/websocket"
)

// Constants for board dimensions.
const (
	numRows = 11
	numCols = 64
)

// Declare the upgrader variable to upgrade HTTP connections to WebSockets.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// In production, you should restrict allowed origins.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Parameters holds global synthesis parameters.
type Parameters struct {
	FilterCutoff float64 `json:"filterCutoff"`
	FilterQ      float64 `json:"filterQ"`
	ReverbDecay  float64 `json:"reverbDecay"`
	Volume       float64 `json:"volume"`
}

// Message represents incoming/outgoing messages.
type Message struct {
	Type       string  `json:"type"`
	Row        int     `json:"row"` // Always send row, even if 0.
	Col        int     `json:"col"` // Always send col, even if 0.
	Value      bool    `json:"value"`
	Param      string  `json:"param,omitempty"`
	ParamValue float64 `json:"paramValue,omitempty"`
	UserColor  string  `json:"userColor,omitempty"`
}

// InboundMessage wraps an incoming message with its originating client.
type InboundMessage struct {
	client *Client
	data   []byte
}

// Client represents a connected client.
type Client struct {
	conn  *websocket.Conn
	send  chan []byte
	color string
}

// Hub maintains the set of active clients, the board state, and parameters.
type Hub struct {
	clients    map[*Client]bool
	inbound    chan InboundMessage
	register   chan *Client
	unregister chan *Client
	boardState [][]bool
	params     Parameters
}

func newHub() *Hub {
	h := &Hub{
		clients:    make(map[*Client]bool),
		inbound:    make(chan InboundMessage),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		boardState: make([][]bool, numRows),
		params: Parameters{
			FilterCutoff: 800,
			FilterQ:      1.0,
			ReverbDecay:  2.0,
			Volume:       1.0,
		},
	}
	for i := 0; i < numRows; i++ {
		h.boardState[i] = make([]bool, numCols)
	}
	return h
}

// broadcastClientCount sends the number of connected clients to all clients.
func (h *Hub) broadcastClientCount() {
	countMsg := struct {
		Type        string `json:"type"`
		ClientCount int    `json:"clientCount"`
	}{
		Type:        "client_count",
		ClientCount: len(h.clients),
	}
	data, err := json.Marshal(countMsg)
	if err != nil {
		log.Printf("Error marshaling client count: %v", err)
		return
	}
	h.broadcast(data)
	log.Printf("Broadcasted client count: %d", len(h.clients))
}

// broadcast sends data to all connected clients.
func (h *Hub) broadcast(data []byte) {
	for client := range h.clients {
		select {
		case client.send <- data:
		default:
			log.Printf("Failed to send message to client %s", client.conn.RemoteAddr())
			close(client.send)
			delete(h.clients, client)
		}
	}
}

// run listens for register, unregister, and inbound messages.
func (h *Hub) run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client] = true
			log.Printf("New client connected: %s (color: %s)", client.conn.RemoteAddr(), client.color)
			h.broadcastClientCount()
			// Send initial state (board and parameters) to the new client.
			initMsg := struct {
				Type      string     `json:"type"`
				GridState [][]bool   `json:"gridState"`
				Params    Parameters `json:"params"`
			}{
				Type:      "init",
				GridState: h.boardState,
				Params:    h.params,
			}
			data, err := json.Marshal(initMsg)
			if err != nil {
				log.Printf("Error marshaling init message: %v", err)
			} else {
				client.send <- data
				log.Printf("Sent init message to %s", client.conn.RemoteAddr())
			}
		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				log.Printf("Client disconnected: %s", client.conn.RemoteAddr())
				delete(h.clients, client)
				close(client.send)
			}
			h.broadcastClientCount()
		case im := <-h.inbound:
			var msg Message
			if err := json.Unmarshal(im.data, &msg); err != nil {
				log.Printf("Error decoding message from %s: %v", im.client.conn.RemoteAddr(), err)
				continue
			}
			log.Printf("Received message from %s: %+v", im.client.conn.RemoteAddr(), msg)
			switch msg.Type {
			case "toggle":
				// Update board state.
				if msg.Row < 0 || msg.Row >= numRows || msg.Col < 0 || msg.Col >= numCols {
					log.Printf("Toggle message out of bounds from %s: row %d, col %d", im.client.conn.RemoteAddr(), msg.Row, msg.Col)
					continue
				}
				h.boardState[msg.Row][msg.Col] = msg.Value
				// Attach the sender's color.
				msg.UserColor = im.client.color
				data, err := json.Marshal(msg)
				if err != nil {
					log.Printf("Error marshaling toggle message: %v", err)
				} else {
					h.broadcast(data)
					log.Printf("Broadcast toggle from %s: row %d, col %d, value %t", im.client.conn.RemoteAddr(), msg.Row, msg.Col, msg.Value)
				}
			case "get_state":
				initMsg := struct {
					Type      string     `json:"type"`
					GridState [][]bool   `json:"gridState"`
					Params    Parameters `json:"params"`
				}{
					Type:      "init",
					GridState: h.boardState,
					Params:    h.params,
				}
				data, err := json.Marshal(initMsg)
				if err != nil {
					log.Printf("Error marshaling init message: %v", err)
				} else {
					im.client.send <- data
					log.Printf("Sent state to %s", im.client.conn.RemoteAddr())
				}
			case "set_param":
				switch msg.Param {
				case "filterCutoff":
					h.params.FilterCutoff = msg.ParamValue
				case "filterQ":
					h.params.FilterQ = msg.ParamValue
				case "reverbDecay":
					h.params.ReverbDecay = msg.ParamValue
				case "volume":
					h.params.Volume = msg.ParamValue
				}
				paramMsg := struct {
					Type   string     `json:"type"`
					Params Parameters `json:"params"`
				}{
					Type:   "param_update",
					Params: h.params,
				}
				data, err := json.Marshal(paramMsg)
				if err != nil {
					log.Printf("Error marshaling param update: %v", err)
				} else {
					h.broadcast(data)
					log.Printf("Broadcast param update from %s: %+v", im.client.conn.RemoteAddr(), h.params)
				}
			default:
				h.broadcast(im.data)
				log.Printf("Broadcast unknown message type: %s", msg.Type)
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
			log.Printf("Read error from %s: %v", c.conn.RemoteAddr(), err)
			break
		}
		log.Printf("Received raw message from %s: %s", c.conn.RemoteAddr(), string(message))
		hub.inbound <- InboundMessage{client: c, data: message}
	}
}

// writePump writes messages from the hub to the client.
func (c *Client) writePump() {
	defer c.conn.Close()
	for message := range c.send {
		if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
			log.Printf("Write error to %s: %v", c.conn.RemoteAddr(), err)
			break
		}
	}
}

// randomColor returns a random hex color string.
func randomColor() string {
	r := rand.Intn(256)
	g := rand.Intn(256)
	b := rand.Intn(256)
	return fmt.Sprintf("#%02X%02X%02X", r, g, b)
}

// serveWs upgrades the HTTP connection to a WebSocket and registers the client.
func serveWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	client := &Client{
		conn:  conn,
		send:  make(chan []byte, 256),
		color: randomColor(),
	}
	hub.register <- client
	log.Printf("Registered client: %s", client.conn.RemoteAddr())
	go client.writePump()
	client.readPump(hub)
}

func main() {
	hub := newHub()
	go hub.run()

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		serveWs(hub, w, r)
	})
	http.Handle("/", http.FileServer(http.Dir("./static")))

	log.Println("Server started on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
