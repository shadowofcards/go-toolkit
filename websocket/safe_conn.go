package websocket

import (
	"sync"
	"time"

	httpws "github.com/gorilla/websocket"
)

type SafeConn struct {
	*httpws.Conn
	mu sync.Mutex
}

func (c *SafeConn) WriteMessage(mt int, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Conn.WriteMessage(mt, data)
}

func (c *SafeConn) WriteControl(mt int, data []byte, deadline time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Conn.WriteControl(mt, data, deadline)
}
