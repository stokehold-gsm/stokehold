// stokehold-sign is used by the release owner on their own machine.
//
//	stokehold-sign keygen <private.pem> <public.pem>
//	stokehold-sign sign   <private.pem> <file> <file.sig>
//	stokehold-sign verify <public.pem>  <file> <file.sig>
//
// The private key never goes into git or CI.
package main

import (
	"fmt"
	"os"

	"github.com/stokehold-gsm/stokehold/pkg/sign"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(a []string) error {
	if len(a) == 0 {
		return fmt.Errorf("usage: keygen | sign | verify (see the file header)")
	}
	switch {
	case a[0] == "keygen" && len(a) == 3:
		if _, err := os.Stat(a[1]); err == nil {
			return fmt.Errorf("%s exists, refusing to overwrite a key", a[1])
		}
		priv, pub, err := sign.GenerateKey()
		if err != nil {
			return err
		}
		if err := os.WriteFile(a[1], priv, 0o600); err != nil {
			return err
		}
		return os.WriteFile(a[2], pub, 0o644)
	case a[0] == "sign" && len(a) == 4:
		kb, err := os.ReadFile(a[1])
		if err != nil {
			return err
		}
		priv, err := sign.ParsePrivate(kb)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(a[2])
		if err != nil {
			return err
		}
		return os.WriteFile(a[3], sign.Sign(priv, data), 0o644)
	case a[0] == "verify" && len(a) == 4:
		kb, err := os.ReadFile(a[1])
		if err != nil {
			return err
		}
		pub, err := sign.ParsePublic(kb)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(a[2])
		if err != nil {
			return err
		}
		sig, err := os.ReadFile(a[3])
		if err != nil {
			return err
		}
		if !sign.Verify(pub, data, sig) {
			return fmt.Errorf("signature does not match")
		}
		fmt.Println("signature ok")
		return nil
	}
	return fmt.Errorf("bad arguments, see the file header")
}
