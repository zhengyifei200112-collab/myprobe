package v2

import (
	"errors"
	"regexp"
)

const ConfigurationCapability = "config_apply.v1"
const TypeConfigurationApplied = "config_applied"
const SamplingConfigurationScope = "sampling_periods"

var configurationNonce = regexp.MustCompile(`^[a-f0-9]{32}$`)

// RuntimeConfiguration is an explicitly negotiated v2 extension. It manages
// periods only; local interface/mount policy must not be cleared by omission.
type RuntimeConfiguration struct {
	Scope             string `json:"scope"`
	Revision          int64  `json:"revision"`
	Nonce             string `json:"nonce"`
	CollectionSeconds int    `json:"collection_seconds"`
	ReportSeconds     int    `json:"report_seconds"`
}

func (c RuntimeConfiguration) Validate() error {
	if c.Scope != SamplingConfigurationScope || c.Revision < 1 || !configurationNonce.MatchString(c.Nonce) || c.CollectionSeconds < 1 || c.CollectionSeconds > 3600 || c.ReportSeconds < 1 || c.ReportSeconds > 3600 {
		return errors.New("invalid_runtime_configuration")
	}
	return nil
}

type ConfigurationApplied RuntimeConfiguration

func (a ConfigurationApplied) Validate() error { return RuntimeConfiguration(a).Validate() }
func AllowsConfiguration(capabilities []string) bool {
	for _, value := range capabilities {
		if value == ConfigurationCapability {
			return true
		}
	}
	return false
}
