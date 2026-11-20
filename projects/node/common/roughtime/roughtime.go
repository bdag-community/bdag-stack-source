//
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package roughtime

import (
	"context"
	"log"
	"time"
)

const RecalibrationInterval = time.Hour

var offset time.Duration

func Init(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	log.Println("Init roughtime... ...")
	recalibrateRoughtime()
	ticker := time.NewTicker(RecalibrationInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				recalibrateRoughtime()
			}
		}
	}()
}

func recalibrateRoughtime() {
}

// Since returns the duration since t, based on the roughtime response
func Since(t time.Time) time.Duration {
	return Now().Sub(t)
}

// Until returns the duration until t, based on the roughtime response
func Until(t time.Time) time.Duration {
	return t.Sub(Now())
}

// Now returns the current local time given the roughtime offset.
func Now() time.Time {
	if offset <= 0 {
		return time.Now()
	}
	return time.Now().Add(offset)
}

func Offset() time.Duration {
	return offset
}
