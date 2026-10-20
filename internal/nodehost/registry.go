package nodehost

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// memRegistry is the default in-memory Registry. Access is serialized by the
// Service mutex, so it carries no lock.
type memRegistry struct {
	codes map[string]time.Time
	nodes map[string]EnrolledNode
}

func newMemRegistry() *memRegistry {
	return &memRegistry{
		codes: make(map[string]time.Time),
		nodes: make(map[string]EnrolledNode),
	}
}

// PutCode stores a one-time enrollment code with its expiry.
func (m *memRegistry) PutCode(code string, expiresAt time.Time) error {
	m.codes[normalizeCode(code)] = expiresAt
	return nil
}

// ConsumeCodeAndPutNode atomically validates/removes a code and stores its node.
func (m *memRegistry) ConsumeCodeAndPutNode(code string, now time.Time, n EnrolledNode) (bool, error) {
	expiresAt, ok := m.codes[code]
	if !ok {
		return false, nil
	}
	if !now.Before(expiresAt) {
		delete(m.codes, code)
		return false, nil
	}
	delete(m.codes, code)
	m.nodes[n.ID] = n
	return true, nil
}

// PutNode upserts an enrolled node by id.
func (m *memRegistry) PutNode(n EnrolledNode) error {
	m.nodes[n.ID] = n
	return nil
}

// Node returns the enrolled node for an id.
func (m *memRegistry) Node(id string) (EnrolledNode, bool, error) {
	n, ok := m.nodes[id]
	return n, ok, nil
}

// DurableRegistryStore is the SQL-owned storage primitive for the nodehost
// registry. nodehost owns the record encoding so this package stays a leaf; the
// store package owns tables, transactions, and migrations.
type DurableRegistryStore interface {
	PutNodehostCode(code string, expiresAt time.Time) error
	ConsumeNodehostCodeAndPutNode(code string, now time.Time, nodeID string, state string, nodeJSON []byte) (bool, error)
	PutNodehostNode(nodeID string, state string, nodeJSON []byte) error
	NodehostNode(nodeID string) ([]byte, bool, error)
}

type storeRegistry struct {
	store DurableRegistryStore
}

// NewStoreRegistry adapts the durable store's nodehost table methods to
// Registry. A nil store falls back to the in-memory registry through
// NewServiceWithRegistry's nil handling.
func NewStoreRegistry(store DurableRegistryStore) Registry {
	if store == nil {
		return nil
	}
	return &storeRegistry{store: store}
}

func (r *storeRegistry) PutCode(code string, expiresAt time.Time) error {
	return r.store.PutNodehostCode(normalizeCode(code), expiresAt)
}

func (r *storeRegistry) ConsumeCodeAndPutNode(code string, now time.Time, n EnrolledNode) (bool, error) {
	data, err := json.Marshal(n)
	if err != nil {
		return false, err
	}
	return r.store.ConsumeNodehostCodeAndPutNode(normalizeCode(code), now, n.ID, string(n.State), data)
}

func (r *storeRegistry) PutNode(n EnrolledNode) error {
	data, err := json.Marshal(n)
	if err != nil {
		return err
	}
	return r.store.PutNodehostNode(n.ID, string(n.State), data)
}

func (r *storeRegistry) Node(id string) (EnrolledNode, bool, error) {
	data, ok, err := r.store.NodehostNode(id)
	if err != nil || !ok {
		return EnrolledNode{}, ok, err
	}
	var n EnrolledNode
	if err := json.Unmarshal(data, &n); err != nil {
		return EnrolledNode{}, false, fmt.Errorf("nodehost registry: corrupt node %s: %w", id, err)
	}
	return n, true, nil
}

type fileRegistry struct {
	path  string
	state registryState
}

type registryState struct {
	Version int                     `json:"version"`
	Codes   map[string]time.Time    `json:"codes"`
	Nodes   map[string]EnrolledNode `json:"nodes"`
}

// OpenFileRegistry opens a durable Registry stored as bounded private JSON.
func OpenFileRegistry(path string) (Registry, error) {
	r := &fileRegistry{
		path: path,
		state: registryState{
			Version: 1,
			Codes:   map[string]time.Time{},
			Nodes:   map[string]EnrolledNode{},
		},
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &r.state); err != nil {
		return nil, fmt.Errorf("nodehost registry: corrupt store %s: %w", path, err)
	}
	if r.state.Codes == nil {
		r.state.Codes = map[string]time.Time{}
	}
	if r.state.Nodes == nil {
		r.state.Nodes = map[string]EnrolledNode{}
	}
	r.state.Version = 1
	return r, nil
}

func (r *fileRegistry) PutCode(code string, expiresAt time.Time) error {
	r.state.Codes[normalizeCode(code)] = expiresAt
	return r.persist()
}

func (r *fileRegistry) ConsumeCodeAndPutNode(code string, now time.Time, n EnrolledNode) (bool, error) {
	expiresAt, ok := r.state.Codes[code]
	if !ok {
		return false, nil
	}
	if !now.Before(expiresAt) {
		delete(r.state.Codes, code)
		return false, r.persist()
	}
	delete(r.state.Codes, code)
	r.state.Nodes[n.ID] = n
	return true, r.persist()
}

func (r *fileRegistry) PutNode(n EnrolledNode) error {
	r.state.Nodes[n.ID] = n
	return r.persist()
}

func (r *fileRegistry) Node(id string) (EnrolledNode, bool, error) {
	n, ok := r.state.Nodes[id]
	return n, ok, nil
}

func (r *fileRegistry) persist() error {
	data, err := json.MarshalIndent(r.state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

func randomID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return "node-" + hex.EncodeToString(data[:]), nil
}

func randomCode() (string, error) {
	var data [10]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(data[:]), nil
}
