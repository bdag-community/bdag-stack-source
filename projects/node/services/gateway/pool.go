package gateway

import (
	"bytes"
	"sync"
)

// dataPool is a sync.Pool for *bytes.Buffer.
var dataPool = sync.Pool{
	New: func() interface{} {
		// Start with distinct buffer capacity to reduce resizing churn
		return bytes.NewBuffer(make([]byte, 0, 4096))
	},
}

// getBuffer retrieves a buffer from the pool.
func getBuffer() *bytes.Buffer {
	return dataPool.Get().(*bytes.Buffer)
}

// putBuffer resets the buffer and returns it to the pool.
func putBuffer(buf *bytes.Buffer) {
	buf.Reset()
	dataPool.Put(buf)
}
