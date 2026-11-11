package api

import (
	"errors"
	"regexp"
)

// validatePattern makes sure a vendor profile regex compiles and has exactly one capture group.
// Go's RE2 engine has linear-time matching, so user patterns cannot cause catastrophic backtracking.
func validatePattern(p string) error {
	if len(p) > 500 {
		return errors.New("pattern too long")
	}
	re, err := regexp.Compile(p)
	if err != nil {
		return err
	}
	if re.NumSubexp() != 1 {
		return errors.New("pattern must contain exactly one capture group")
	}
	return nil
}
