// Package config loads exporter configuration from a YAML file with
// environment variable overrides for secrets and deployment-specific values.
package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Statsd    Statsd    `yaml:"statsd"`
	UniFi     UniFi     `yaml:"unifi"`
	SNMPTraps SNMPTraps `yaml:"snmpTraps"`
}

type Statsd struct {
	// Address is the host:port of the DogStatsD/Telegraf statsd listener.
	Address string `yaml:"address"`
	// Namespace is prefixed to every metric name, e.g. "unifi.".
	Namespace string `yaml:"namespace"`
	// Tags are added to every metric, in "key:value" form.
	Tags []string `yaml:"tags"`
}

type UniFi struct {
	// URL is the base URL of the UniFi OS console, e.g. https://192.168.1.1.
	URL  string `yaml:"url"`
	Site string `yaml:"site"`
	// APIKey is preferred; Username/Password are used when it is empty.
	APIKey             string        `yaml:"apiKey"`
	Username           string        `yaml:"username"`
	Password           string        `yaml:"password"`
	InsecureSkipVerify bool          `yaml:"insecureSkipVerify"`
	PollInterval       time.Duration `yaml:"pollInterval"`
	Timeout            time.Duration `yaml:"timeout"`
}

type SNMPTraps struct {
	Enabled bool `yaml:"enabled"`
	// ListenAddress is the UDP address to receive traps on. Defaults to a
	// non-privileged port; map 162 to it at the Kubernetes Service.
	ListenAddress string `yaml:"listenAddress"`
	// Community, when set, drops v1/v2c traps with a different community.
	Community string `yaml:"community"`
}

func Default() Config {
	return Config{
		Statsd: Statsd{
			Address:   "127.0.0.1:8125",
			Namespace: "unifi.",
		},
		UniFi: UniFi{
			Site:               "default",
			InsecureSkipVerify: true,
			PollInterval:       30 * time.Second,
			Timeout:            10 * time.Second,
		},
		SNMPTraps: SNMPTraps{
			ListenAddress: "0.0.0.0:1162",
		},
	}
}

// Load reads the YAML file at path (if non-empty) over the defaults, then
// applies environment overrides and validates the result.
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return cfg, fmt.Errorf("reading config: %w", err)
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("parsing config: %w", err)
		}
	}
	applyEnv(&cfg)
	return cfg, cfg.Validate()
}

func applyEnv(cfg *Config) {
	overrides := map[string]*string{
		"STATSD_ADDRESS":      &cfg.Statsd.Address,
		"UNIFI_URL":           &cfg.UniFi.URL,
		"UNIFI_SITE":          &cfg.UniFi.Site,
		"UNIFI_API_KEY":       &cfg.UniFi.APIKey,
		"UNIFI_USERNAME":      &cfg.UniFi.Username,
		"UNIFI_PASSWORD":      &cfg.UniFi.Password,
		"SNMP_TRAP_COMMUNITY": &cfg.SNMPTraps.Community,
	}
	for env, field := range overrides {
		if v, ok := os.LookupEnv(env); ok && v != "" {
			*field = v
		}
	}
}

func (c Config) Validate() error {
	var errs []error
	if c.Statsd.Address == "" {
		errs = append(errs, errors.New("statsd.address is required"))
	}
	if c.UniFi.URL == "" {
		errs = append(errs, errors.New("unifi.url is required"))
	}
	if c.UniFi.APIKey == "" && (c.UniFi.Username == "" || c.UniFi.Password == "") {
		errs = append(errs, errors.New("unifi.apiKey or unifi.username/password is required"))
	}
	if c.UniFi.PollInterval <= 0 {
		errs = append(errs, errors.New("unifi.pollInterval must be positive"))
	}
	if c.SNMPTraps.Enabled && c.SNMPTraps.ListenAddress == "" {
		errs = append(errs, errors.New("snmpTraps.listenAddress is required when traps are enabled"))
	}
	return errors.Join(errs...)
}
