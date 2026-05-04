//go:build !windows

package iconext

func extractImpl(exePath string) ([]byte, error) {
	return nil, ErrUnsupported
}
