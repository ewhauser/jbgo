package interp

import "sync"

type printfEnvCache struct {
	mu     sync.RWMutex
	values map[string]string
}

func newPrintfEnvCache() *printfEnvCache {
	return &printfEnvCache{values: make(map[string]string)}
}

func (c *printfEnvCache) get(name string) (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.RLock()
	value, ok := c.values[name]
	c.mu.RUnlock()
	return value, ok
}

func (c *printfEnvCache) getOrStore(name, value string) string {
	if c == nil {
		return value
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.values == nil {
		c.values = make(map[string]string)
	}
	if cached, ok := c.values[name]; ok {
		return cached
	}
	c.values[name] = value
	return value
}
