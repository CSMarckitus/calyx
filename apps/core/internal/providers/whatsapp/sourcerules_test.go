package whatsapp

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// whatsmeow keeps the label and the payload in package variables that every instance shares. The
// core changes them only through the payload hook of each client, so no code may name them.
var forbiddenNames = []string{
	"store.SetOSInfo", "store.DeviceProps", "store.BaseClientPayload", "store.SetWAVersion",
	"SendPasskeyResponse", "SendPasskeyConfirmation",
}

func TestNoCodeUsesTheSharedPayloadOfWhatsmeow(t *testing.T) {
	for _, root := range []string{"../../../internal", "../../../cmd"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, name := range forbiddenNames {
				if strings.Contains(string(source), name) {
					t.Errorf("%s uses %s", path, name)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
