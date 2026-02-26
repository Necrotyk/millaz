package conspiribot

// Config matches the overall JSON structure
type Config struct {
	Server    GlobalServerConfig `json:"server"`
	Scheduler SchedulerConfig    `json:"scheduler"`
	Bots      []BotPersona       `json:"bots"`
	AdminNick string             `json:"admin_nick,omitempty"`
}

type SchedulerConfig struct {
	IntervalSeconds    float64 `json:"interval_seconds"`
	QueueSize          int     `json:"queue_size"`
	StaggerTimeSeconds float64 `json:"stagger_time_seconds"`
}

type GlobalServerConfig struct {
	Host               string `json:"host"`
	Port               int    `json:"port"`
	Channel            string `json:"channel"`
	UseTLS             bool   `json:"use_tls"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitempty"`
}

// AppConfig is the globally loaded configuration (read-only for agents)
var AppConfig *Config

// BotPersona matches the structure of each bot in the config
type BotPersona struct {
	Nick     string   `json:"nick"`
	Type     string   `json:"type"`  // "persona" (default) or "utility"
	Model    *string  `json:"model"` // Use pointer for nullable fields
	APIType  string   `json:"api_type"`
	Triggers []string `json:"triggers"`
	System   string   `json:"system"`
	// ReplyProbability: chance (0.0-1.0) the bot will issue a shallow reply to any message
	ReplyProbability float64 `json:"reply_probability,omitempty"`
	// ReplyCooldownSeconds: minimum seconds between bot's own messages
	ReplyCooldownSeconds int `json:"reply_cooldown_seconds,omitempty"`
	// Optional: bind outgoing connection to this source IP (requires privileges)
	SourceIP string `json:"source_ip,omitempty"`
	// If false, the bot will stay connected but won't speak until enabled
	Enabled *bool `json:"enabled,omitempty"`

	// SASL Authentication
	SASLUser     string `json:"sasl_user,omitempty"`
	SASLPassword string `json:"sasl_password,omitempty"`
}

// LoadConfig is deprecated. Configuration is now provided by the main application via Init
