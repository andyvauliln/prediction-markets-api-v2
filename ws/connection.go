package ws

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/andyvauliln/prediction-markets-api/types"
	"github.com/andyvauliln/prediction-markets-api/utils"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gorilla/websocket"
)

const (
	MarketChannel = "markets"
)

var logger = utils.Logger

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type Conn struct {
	*websocket.Conn
	mu sync.Mutex
}

var connectionUnsubscribtions map[*Conn][]func(*Conn)
var socketChannels map[string]func(interface{}, *Conn)

// ConnectionEndpoint is the the handleFunc function for websocket connections
// It handles incoming websocket messages and routes the message according to
// channel parameter in channelMessage
func ConnectionEndpoint(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.Error(err)
		return
	}

	conn := &Conn{c, sync.Mutex{}}
	initConnection(conn)

	go func() {
		// Recover in case of any panic in websocket. So that the app doesn't crash ===
		defer func() {
			if r := recover(); r != nil {
				err, ok := r.(error)
				if err != nil {
					logger.Error(err)
				}

				if !ok {
					logger.Error("Failed attempt at recovering websocket panic")
				}
			}
		}()

		for {
			messageType, p, err := conn.ReadMessage()
			if err != nil {
				logger.Error(err)
				conn.Close()
			}

			if messageType != 1 {
				return
			}

			msg := types.WebSocketMessage{}
			if err := json.Unmarshal(p, &msg); err != nil {
				logger.Error(err)
				SendMessage(conn, msg.Channel, "ERROR", err.Error())
				return
			}

			conn.SetCloseHandler(wsCloseHandler(conn))

			if socketChannels[msg.Channel] == nil {
				SendMessage(conn, msg.Channel, "ERROR", "INVALID_CHANNEL")
			}

			go socketChannels[msg.Channel](msg.Payload, conn)
		}
	}()
}

// Create new connection to web socket
func NewConnection(conn *websocket.Conn) *Conn {
	return &Conn{conn, sync.Mutex{}}
}

// initConnection initializes connection in connectionUnsubscribtions map
func initConnection(conn *Conn) {
	if connectionUnsubscribtions == nil {
		connectionUnsubscribtions = make(map[*Conn][]func(*Conn))
	}

	if connectionUnsubscribtions[conn] == nil {
		connectionUnsubscribtions[conn] = make([]func(*Conn), 0)
	}
}

// RegisterChannel function needs to be called whenever the system is interested in listening to
// a new channel. A channel needs function which will handle the incoming messages for that channel.
//
// channelMessage handler function receives message from channelMessage and pointer to connection
func RegisterChannel(channel string, fn func(interface{}, *Conn)) error {
	if channel == "" {
		return errors.New("Channel can not be empty string")
	}

	if fn == nil {
		logger.Error("Handler should not be nil")
		return errors.New("Handler should not be nil")
	}

	ch := getChannelMap()
	if ch[channel] != nil {
		logger.Error("Channel already registered")
		return fmt.Errorf("Channel already registered")
	}

	ch[channel] = fn
	return nil
}

// getChannelMap returns singleton map of channels with there handler functions
func getChannelMap() map[string]func(interface{}, *Conn) {
	if socketChannels == nil {
		socketChannels = make(map[string]func(interface{}, *Conn))
	}
	return socketChannels
}

// RegisterConnectionUnsubscribeHandler needs to be called whenever a connection subscribes to
// a new channel.
// At the time of connection closing the ConnectionUnsubscribeHandler handlers associated with
// that connection are triggered.
func RegisterConnectionUnsubscribeHandler(conn *Conn, fn func(*Conn)) {
	connectionUnsubscribtions[conn] = append(connectionUnsubscribtions[conn], fn)
}

// wsCloseHandler handles the closing of connection.
// it triggers all the UnsubscribeHandler associated with the closing
// connection in a separate go routine
func wsCloseHandler(conn *Conn) func(code int, text string) error {
	return func(code int, text string) error {
		for _, unsub := range connectionUnsubscribtions[conn] {
			go unsub(conn)
		}
		return nil
	}
}

// SendMessage constructs the message with proper structure to be sent over websocket
func SendMessage(conn *Conn, channel string, msgType string, data interface{}, hash ...common.Hash) {
	payload := types.WebSocketPayload{
		Type: msgType,
		Data: data,
	}

	message := types.WebSocketMessage{
		Channel: channel,
		Payload: payload,
	}

	conn.mu.Lock()
	defer conn.mu.Unlock()
	err := conn.WriteJSON(message)
	if err != nil {
		logger.Error(err)
		conn.Close()
	}
}
