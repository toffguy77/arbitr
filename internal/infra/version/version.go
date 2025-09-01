package version

import (
    "encoding/json"
    "net/http"
)

var (
    Version   = "dev"
    Commit    = "none"
    BuildTime = "unknown"
)

type info struct {
    Version   string `json:"version"`
    Commit    string `json:"commit"`
    BuildTime string `json:"build_time"`
}

// Handler writes version info as JSON
func Handler(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(info{Version: Version, Commit: Commit, BuildTime: BuildTime})
}
