//go:build !tinygo && !force_tinygo_logic

package aws

import "github.com/shibukawa/tinygodriver/internal/cloudhttp"

// Backend identifies the HTTP stack selected by build constraints.
const Backend = cloudhttp.Backend
