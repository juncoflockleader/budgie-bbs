package core

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// NewSessionNodeID generates a unique ID for an SSH session node.
func NewSessionNodeID() string { return newID("sess_") }

// NodeEntry is a snapshot of an active SSH session visible to sysops.
type NodeEntry struct {
	NodeID    string    `json:"nodeId"`
	UserID    string    `json:"userId"`
	Username  string    `json:"username"`
	RemoteIP  string    `json:"remoteIp,omitempty"`
	Location  string    `json:"location"`  // current TUI page label
	LoginTime time.Time `json:"loginTime"`
}

// nodeSession holds the runtime handles for a live SSH session.
type nodeSession struct {
	NodeEntry
	cancelFn func()     // calls sess.Close() to kick the SSH connection
	msgCh    chan string // receives sysop messages directed at this node
}

// NodeRegistry tracks active SSH sessions in memory. It is safe for concurrent
// use. The registry has no persistence: it is rebuilt from live sessions on
// each process start.
type NodeRegistry struct {
	mu       sync.RWMutex
	sessions map[string]*nodeSession
	bus      Bus
}

func newNodeRegistry(bus Bus) *NodeRegistry {
	return &NodeRegistry{
		sessions: make(map[string]*nodeSession),
		bus:      bus,
	}
}

// Register adds a session to the registry, publishes EvtNodeConnected on the
// bus, and returns a read-only channel the TUI can poll for incoming sysop
// messages. cancelFn is called by KickNode to forcibly close the SSH session.
func (r *NodeRegistry) Register(entry NodeEntry, cancelFn func()) <-chan string {
	msgCh := make(chan string, 8)
	r.mu.Lock()
	r.sessions[entry.NodeID] = &nodeSession{
		NodeEntry: entry,
		cancelFn:  cancelFn,
		msgCh:     msgCh,
	}
	r.mu.Unlock()

	r.bus.Publish(&proto.Event{
		Kind: proto.EvtNodeConnected,
		Payload: &proto.NodeConnectedPayload{
			NodeID:   entry.NodeID,
			User:     entry.Username,
			RemoteIP: entry.RemoteIP,
			TS:       time.Now().UnixMilli(),
		},
		Scopes: []string{"system:nodes"},
		TS:     time.Now().UnixMilli(),
	})
	return msgCh
}

// Unregister removes a session and publishes EvtNodeDisconnected on the bus.
// Safe to call more than once; subsequent calls are no-ops.
func (r *NodeRegistry) Unregister(nodeID string) {
	r.mu.Lock()
	s, ok := r.sessions[nodeID]
	if ok {
		delete(r.sessions, nodeID)
	}
	r.mu.Unlock()
	if !ok {
		return
	}
	r.bus.Publish(&proto.Event{
		Kind: proto.EvtNodeDisconnected,
		Payload: &proto.NodeDisconnectedPayload{
			NodeID: nodeID,
			User:   s.Username,
			TS:     time.Now().UnixMilli(),
		},
		Scopes: []string{"system:nodes"},
		TS:     time.Now().UnixMilli(),
	})
}

// UpdateLocation sets the current page label for a session. Non-blocking; if
// the session is no longer present the call is silently ignored.
func (r *NodeRegistry) UpdateLocation(nodeID, location string) {
	r.mu.Lock()
	if s, ok := r.sessions[nodeID]; ok {
		s.Location = location
	}
	r.mu.Unlock()
}

// List returns a snapshot of all active nodes sorted by login time (oldest
// first, matching classic BBS node-spy convention).
func (r *NodeRegistry) List() []NodeEntry {
	r.mu.RLock()
	entries := make([]NodeEntry, 0, len(r.sessions))
	for _, s := range r.sessions {
		entries = append(entries, s.NodeEntry)
	}
	r.mu.RUnlock()

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].LoginTime.Before(entries[j].LoginTime)
	})
	return entries
}

// KickNode cancels the SSH session for the given nodeID by calling its close
// function. The session is removed from the registry when the SSH goroutine
// notices the connection has closed and calls Unregister.
func (r *NodeRegistry) KickNode(nodeID string) error {
	r.mu.RLock()
	s, ok := r.sessions[nodeID]
	r.mu.RUnlock()
	if !ok {
		return errors.New("node not found")
	}
	s.cancelFn()
	return nil
}

// SendMessage enqueues a sysop message to the given node. The receiving TUI
// session polls this channel and displays the message in its status bar.
// Returns an error if the node is not found or the queue is full.
func (r *NodeRegistry) SendMessage(nodeID, msg string) error {
	r.mu.RLock()
	s, ok := r.sessions[nodeID]
	r.mu.RUnlock()
	if !ok {
		return errors.New("node not found")
	}
	select {
	case s.msgCh <- msg:
		return nil
	default:
		return errors.New("node message queue full")
	}
}
