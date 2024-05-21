package internal

import (
	"github.com/icinga/icinga-go-library/database"
	"github.com/icinga/icinga-kubernetes/pkg/metrics"
)

// Config defines Icinga Kubernetes config.
type Config struct {
	Database   database.Config          `yaml:"database"`
	Prometheus metrics.PrometheusConfig `yaml:"prometheus"`
}

// Validate checks constraints in the supplied configuration and returns an error if they are violated.
func (c *Config) Validate() error {
	if err := c.Database.Validate(); err != nil {
		return err
	}

	if err := c.Prometheus.Validate(); err != nil {
		return err
	}

	return nil
}
