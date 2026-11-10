package protocol

import (
	"fmt"
	"strings"
)

func validatePaymentProviderMethods(manifest Manifest, provider ManifestPaymentProvider) error {
	methods := make(map[string]struct{}, len(provider.Methods))
	for _, method := range provider.Methods {
		if !ValidPaymentMethodID(method.ID) || strings.TrimSpace(method.Label) == "" {
			return fmt.Errorf("payment provider contribution %q has invalid method %q", provider.ID, method.ID)
		}
		if _, exists := methods[method.ID]; exists {
			return fmt.Errorf("payment provider contribution %q has duplicate method %q", provider.ID, method.ID)
		}
		methods[method.ID] = struct{}{}
	}
	if field := strings.TrimSpace(provider.MethodsField); field != "" && !manifestHasConfigField(manifest, field) {
		return fmt.Errorf("payment provider contribution %q methods field %q is not a configuration field", provider.ID, field)
	}
	return nil
}
