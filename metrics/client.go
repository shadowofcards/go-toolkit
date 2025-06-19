package metrics

import (
	"context"
	"time"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/influxdata/influxdb-client-go/v2/api"
	"github.com/shadowofcards/go-toolkit/contexts"
)

type Recorder interface {
	Inc(ctx context.Context, name string, delta int64) error
	Gauge(ctx context.Context, name string, value float64) error

	IncWithTags(ctx context.Context, name string, delta int64, tags map[string]string) error
	GaugeWithTags(ctx context.Context, name string, value float64, tags map[string]string) error
}

type Factory interface {
	NewRecorder(opts ...Option) (Recorder, error)
}

type Option func(*Config)

type Config struct {
	InfluxURL        string
	Token            string
	Org              string
	Bucket           string
	FlushInterval    time.Duration
	DefaultTags      map[string]string
	ExtraTags        map[string]string
	MaxRetentionDays int
}

type Client struct {
	writeAPI api.WriteAPIBlocking
	cfg      Config
}

func New(opts ...Option) (*Client, error) {
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

var _ Recorder = (*Client)(nil)

func WithURL(u string) Option    { return func(c *Config) { c.InfluxURL = u } }
func WithToken(t string) Option  { return func(c *Config) { c.Token = t } }
func WithOrg(o string) Option    { return func(c *Config) { c.Org = o } }
func WithBucket(b string) Option { return func(c *Config) { c.Bucket = b } }
func WithDefaultTags(tags map[string]string) Option {
	return func(c *Config) {
		for k, v := range tags {
			c.DefaultTags[k] = v
		}
	}
}
func WithExtraTags(tags map[string]string) Option {
	return func(c *Config) {
		for k, v := range tags {
			c.ExtraTags[k] = v
		}
	}
}

func (c *Client) Inc(ctx context.Context, name string, delta int64) error {
	return c.write(ctx, name, map[string]interface{}{"count": delta}, nil)
}

func (c *Client) Gauge(ctx context.Context, name string, value float64) error {
	return c.write(ctx, name, map[string]interface{}{"value": value}, nil)
}

func (c *Client) IncWithTags(ctx context.Context, name string, delta int64, extra map[string]string) error {
	return c.write(ctx, name, map[string]interface{}{"count": delta}, extra)
}

func (c *Client) GaugeWithTags(ctx context.Context, name string, value float64, extra map[string]string) error {
	return c.write(ctx, name, map[string]interface{}{"value": value}, extra)
}

func (c *Client) write(ctx context.Context, measurement string, fields map[string]interface{}, extra map[string]string) error {
	tags := make(map[string]string, len(c.cfg.DefaultTags)+len(c.cfg.ExtraTags)+len(extra)+2)
	for k, v := range c.cfg.DefaultTags {
		tags[k] = v
	}
	for k, v := range c.cfg.ExtraTags {
		tags[k] = v
	}
	for k, v := range extra {
		tags[k] = v
	}

	if v := ctx.Value(contexts.KeyTenantID); v != nil {
		if s, ok := v.(string); ok && s != "" {
			tags["tenant_id"] = s
		}
	}
	if v := ctx.Value(contexts.KeyRegion); v != nil {
		if s, ok := v.(string); ok && s != "" {
			tags["region"] = s
		}
	}

	point := influxdb2.NewPoint(measurement, tags, fields, time.Now().UTC())
	return c.writeAPI.WritePoint(ctx, point)
}
