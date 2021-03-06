/*
MIT License

Copyright (c) 2018 Victor Springer

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package cache

import (
	"bytes"
	"encoding/gob"
	"errors"
	"hash/fnv"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"time"
)

// Response is the cached response data structure.
type Response struct {
	// Value is the cached response value.
	Value []byte

	// Expiration is the cached response expiration date.
	Expiration time.Time

	// LastAccess is the last date a cached response was accessed.
	// Used by LRU and MRU algorithms.
	LastAccess time.Time

	// Frequency is the count of times a cached response is accessed.
	// Used for LFU and MFU algorithms.
	Frequency int
}

// Config contains the Client configuration parameters.
// ReleaseKey is optional setting.
type Config struct {
	// Adapter type for the HTTP cache middleware client.
	Adapter Adapter

	// TTL is how long a response is going to be cached.
	TTL time.Duration

	// ReleaseKey is the parameter key used to free a request cached
	// response. Optional setting.
	ReleaseKey string
}

// Client data structure for HTTP cache middleware.
type Client struct {
	adapter    Adapter
	ttl        time.Duration
	releaseKey string
}

// Adapter interface for HTTP cache middleware client.
type Adapter interface {
	// Get retrieves the cached response by a given key. It also
	// returns true or false, whether it exists or not.
	Get(key uint64) ([]byte, bool)

	// Set caches a response for a given key until an expiration date.
	Set(key uint64, response []byte, expiration time.Time)

	// Release frees cache for a given key.
	Release(key uint64)
}

// Middleware is the HTTP cache middleware handler.
func (c *Client) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" || r.Method == "" {
			sortURLParams(r.URL)
			key := generateKey(r.URL.String())

			params := r.URL.Query()
			if _, ok := params[c.releaseKey]; ok {
				delete(params, c.releaseKey)

				r.URL.RawQuery = params.Encode()
				key = generateKey(r.URL.String())

				c.adapter.Release(key)
			} else {
				b, ok := c.adapter.Get(key)
				response := BytesToResponse(b)
				if ok {
					if response.Expiration.After(time.Now()) {
						response.LastAccess = time.Now()
						response.Frequency++
						c.adapter.Set(key, response.Bytes(), response.Expiration)

						w.WriteHeader(http.StatusFound)
						w.Write(response.Value)
						return
					}

					c.adapter.Release(key)
				}
			}

			rec := httptest.NewRecorder()
			next.ServeHTTP(rec, r)

			statusCode := rec.Result().StatusCode
			if statusCode < 400 {
				now := time.Now()
				value := rec.Body.Bytes()

				response := Response{
					Value:      value,
					Expiration: now.Add(c.ttl),
					LastAccess: now,
					Frequency:  1,
				}
				c.adapter.Set(key, response.Bytes(), response.Expiration)

				w.WriteHeader(statusCode)
				w.Write(value)
			}
		}
	})
}

// BytesToResponse converts bytes array into Response data structure.
func BytesToResponse(b []byte) Response {
	var r Response
	dec := gob.NewDecoder(bytes.NewReader(b))
	dec.Decode(&r)

	return r
}

// Bytes converts Response data structure into bytes array.
func (r Response) Bytes() []byte {
	var b bytes.Buffer
	enc := gob.NewEncoder(&b)
	enc.Encode(&r)

	return b.Bytes()
}

func sortURLParams(URL *url.URL) {
	params := URL.Query()
	for _, param := range params {
		sort.Slice(param, func(i, j int) bool {
			return param[i] < param[j]
		})
	}
	URL.RawQuery = params.Encode()
}

func generateKey(URL string) uint64 {
	hash := fnv.New64a()
	hash.Write([]byte(URL))

	return hash.Sum64()
}

// NewClient initializes the cache HTTP middleware client with a given
// configuration.
func NewClient(cfg *Config) (*Client, error) {
	if cfg.Adapter == nil {
		return nil, errors.New("cache client requires an adapter")
	}

	if int64(cfg.TTL) < 1 {
		return nil, errors.New("cache client requires a valid ttl")
	}

	c := &Client{
		adapter:    cfg.Adapter,
		ttl:        cfg.TTL,
		releaseKey: cfg.ReleaseKey,
	}

	return c, nil
}
