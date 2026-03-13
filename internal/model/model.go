package model

type ProviderSource struct {
	Provider   string            `json:"provider"`
	SourceURL  string            `json:"source_url"`
	SourceType string            `json:"source_type"` // html | json | direct
	Enabled    bool              `json:"enabled"`
	Headers    map[string]string `json:"headers"`
}

type GameSpec struct {
	Provider     string   `json:"provider"`
	GameName     string   `json:"game_name"`
	EnglishTitle string   `json:"english_title"`
	OutputSlug   string   `json:"output_slug"`
	Aliases      []string `json:"aliases"`
}

type SizeSpec struct {
	Name   string `json:"name"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type RetrySpec struct {
	MaxAttempts   int `json:"max_attempts"`
	BackoffMillis int `json:"backoff_millis"`
}

type ExtractSpec struct {
	MaxAssetsPerProvider int `json:"max_assets_per_provider"`
	MinimumMatchScore    int `json:"minimum_match_score"`
}

type ServerSpec struct {
	Addr string `json:"addr"`
}

type Config struct {
	ProvidersPath  string     `json:"providers_path"`
	GamesPath      string     `json:"games_path"`
	OutputDir      string     `json:"output_dir"`
	WorkDir        string     `json:"work_dir"`
	IncomingDir    string     `json:"incoming_dir"`
	UserAgent      string     `json:"user_agent"`
	TimeoutSeconds int        `json:"timeout_seconds"`
	Retry          RetrySpec  `json:"retry"`
	Sizes          []SizeSpec `json:"sizes"`
	Extract        ExtractSpec`json:"extract"`
	Server         ServerSpec `json:"server"`
}

type AssetCandidate struct {
	Provider string
	Source   string // provider source url
	URL      string
	Title    string
	Alt      string
	FileName string
	Width    int
	Height   int
}

type MatchResult struct {
	Game        GameSpec
	Candidate   *AssetCandidate
	Score       int
	Reason      string
	NeedsEdit   bool
	Downloaded  string // local master path
	Outputs     []string
	Error       string
}
