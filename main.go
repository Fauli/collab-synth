package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// Constants for board dimensions.
const (
	numRows = 11
	numCols = 64
)

// Parameters holds global synthesis parameters.
type Parameters struct {
	FilterCutoff float64 `json:"filterCutoff"`
	FilterQ      float64 `json:"filterQ"`
	ReverbDecay  float64 `json:"reverbDecay"`
	Volume       float64 `json:"volume"`
}

// --- WebSocket Upgrade Configuration ---
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// In production, restrict allowed origins.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Message represents incoming/outgoing messages.
type Message struct {
	Type       string  `json:"type"`
	Row        int     `json:"row"`
	Col        int     `json:"col"`
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

// --- Client Structure ---
type Client struct {
	conn  *websocket.Conn
	send  chan []byte
	color string
}

// --- Hub Structure ---
type Hub struct {
	clients    map[*Client]bool
	inbound    chan InboundMessage
	register   chan *Client
	unregister chan *Client
	boardState [][]bool
	params     Parameters
}

// newHub initializes a new hub with an empty board and default parameters.
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

// randomColor generates a random hex color code.
func randomColor() string {
	r := rand.Intn(256)
	g := rand.Intn(256)
	b := rand.Intn(256)
	return fmt.Sprintf("#%02X%02X%02X", r, g, b)
}

// run listens for register, unregister, and inbound messages.
func (h *Hub) run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client] = true
			// Send current board state and parameters.
			initMsg := struct {
				Type      string     `json:"type"`
				GridState [][]bool   `json:"gridState"`
				Params    Parameters `json:"params"`
			}{
				Type:      "init",
				GridState: h.boardState,
				Params:    h.params,
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
				// Set the user color from the originating client.
				msg.UserColor = im.client.color
				// Marshal the modified message.
				if data, err := json.Marshal(msg); err != nil {
					log.Println("Error marshaling toggle message:", err)
				} else {
					h.broadcast(data)
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
				if data, err := json.Marshal(initMsg); err != nil {
					log.Println("Error marshaling init message:", err)
				} else {
					im.client.send <- data
				}

			case "set_param":
				// Update parameter based on msg.Param.
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
				// Broadcast new parameter state.
				paramMsg := struct {
					Type   string     `json:"type"`
					Params Parameters `json:"params"`
				}{
					Type:   "param_update",
					Params: h.params,
				}
				if data, err := json.Marshal(paramMsg); err != nil {
					log.Println("Error marshaling param update:", err)
				} else {
					h.broadcast(data)
				}

			default:
				// For any other message types, broadcast.
				h.broadcast(im.data)
			}
		}
	}
}

// broadcast sends data to all connected clients.
func (h *Hub) broadcast(data []byte) {
	for client := range h.clients {
		select {
		case client.send <- data:
		default:
			close(client.send)
			delete(h.clients, client)
		}
	}
}

// readPump reads messages from a client and sends them to hub.inbound.
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

// writePump writes messages from the hub to a client.
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
	// Seed random generator (ideally do this once in main or init).
	rand.Seed(time.Now().UnixNano())
	client := &Client{
		conn:  conn,
		send:  make(chan []byte, 256),
		color: randomColor(),
	}
	hub.register <- client

	go client.writePump()
	client.readPump(hub)
}

func main() {
	hub := newHub()
	go hub.run()

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		serveWs(hub, w, r)
	})

	// Serve static files (frontend).
	http.Handle("/", http.FileServer(http.Dir("./static")))

	fmt.Println("Server started on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

