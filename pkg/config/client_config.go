package config

// Config describes client config persisted on disk.
type Config struct {
	RootURL string `json:"root_url"`
	Arch    string `json:"cur_arch"`
	// absolute path
	OhosSdk string `json:"ohos_sdk"`
	Channel string `json:"channel"`
}
