package metrics

import (
	"context"
	"time"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/influxdata/influxdb-client-go/v2/api"
	"github.com/shadowofcards/go-toolkit/contexts"
)

//
// Interfaces
//

// Recorder defines the methods for recording metrics.
type Recorder interface {
	// Inc increments a counter metric by delta.
	Inc(ctx context.Context, name string, delta int64) error
	// Gauge records a gauge metric to the given value.
	Gauge(ctx context.Context, name string, value float64) error
}

// Factory knows how to create a Recorder instance.
type Factory interface {
	// NewRecorder constructs and returns a Recorder.
	NewRecorder(opts ...Option) (Recorder, error)
}

//
// Configuration and Client
//

// Option configures the metrics client behavior.
type Option func(*Config)

// Config holds configuration for the InfluxDB metrics client.
type Config struct {
	InfluxURL        string
	Token            string
	Org              string
	Bucket           string
	FlushInterval    time.Duration
	DefaultTags      map[string]string
	ExtraTags        map[string]string
	MaxRetentionDays int // document-only; bucket retention must be set externally
}

// Client is the InfluxDB-backed implementation of Recorder.
type Client struct {
	writeAPI api.WriteAPIBlocking
	cfg      Config
}

// New constructs a Client which implements Recorder.
func New(opts ...Option) (*Client, error) {
	// default configuration
	cfg := Config{
		FlushInterval:    30 * time.Second,
		DefaultTags:      map[string]string{},
		ExtraTags:        map[string]string{},
		MaxRetentionDays: 180,
	}
	for _, o := range opts {
		o(&cfg)
	}

	cli := influxdb2.NewClient(cfg.InfluxURL, cfg.Token)
	writeAPI := cli.WriteAPIBlocking(cfg.Org, cfg.Bucket)
	return &Client{writeAPI: writeAPI, cfg: cfg}, nil
}

// Ensure Client implements Recorder.
var _ Recorder = (*Client)(nil)

// WithURL sets the InfluxDB server URL.
func WithURL(u string) Option {
	return func(c *Config) { c.InfluxURL = u }
}

// WithToken sets the authentication token.
func WithToken(t string) Option {
	return func(c *Config) { c.Token = t }
}

// WithOrg sets the InfluxDB organization.
func WithOrg(o string) Option {
	return func(c *Config) { c.Org = o }
}

// WithBucket sets the InfluxDB bucket.
func WithBucket(b string) Option {
	return func(c *Config) { c.Bucket = b }
}

// WithDefaultTags adds static tags to every metric.
func WithDefaultTags(tags map[string]string) Option {
	return func(c *Config) {
		for k, v := range tags {
			c.DefaultTags[k] = v
		}
	}
}

// WithExtraTags adds additional fixed tags.
func WithExtraTags(tags map[string]string) Option {
	return func(c *Config) {
		for k, v := range tags {
			c.ExtraTags[k] = v
		}
	}
}

//
// Recorder methods
//

// Inc increments a counter metric by delta.
// Internally writes field "count".
// Tags: DefaultTags, ExtraTags, tenant_id, region.
func (c *Client) Inc(ctx context.Context, name string, delta int64) error {
	return c.write(ctx, name, map[string]interface{}{"count": delta})
}

// Gauge records a gauge metric to the given value.
// Internally writes field "value".
// Tags: DefaultTags, ExtraTags, tenant_id, region.
func (c *Client) Gauge(ctx context.Context, name string, value float64) error {
	return c.write(ctx, name, map[string]interface{}{"value": value})
}

// write merges configuration tags and context tags (tenant_id, region),
// constructs an InfluxDB point, and sends it.
func (c *Client) write(ctx context.Context, measurement string, fields map[string]interface{}) error {
	// Merge default and extra tags
	tags := make(map[string]string, len(c.cfg.DefaultTags)+len(c.cfg.ExtraTags)+2)
	for k, v := range c.cfg.DefaultTags {
		tags[k] = v
	}
	for k, v := range c.cfg.ExtraTags {
		tags[k] = v
	}
	// tenant_id tag (mandatory for per-tenant filtering)
	if v := ctx.Value(contexts.KeyTenantID); v != nil {
		if s, ok := v.(string); ok && s != "" {
			tags["tenant_id"] = s
		}
	}
	// region tag (optional geographic grouping)
	if v := ctx.Value(contexts.KeyRegion); v != nil {
		if s, ok := v.(string); ok && s != "" {
			tags["region"] = s
		}
	}

	point := influxdb2.NewPoint(measurement, tags, fields, time.Now())
	return c.writeAPI.WritePoint(ctx, point)
}
