package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// resolveStatePaths picks where the Telegram offset (state.json) and the dead-letter log live.
//
// Default (TG_STATE_FILE and TG_DEAD_LETTER_FILE unset): exactly as before, inside dir. Nothing changes.
//
// With TG_STATE_FILE set to an absolute path outside the deployment folder, the offset lives there so that
// replacing or deleting the deployment folder cannot lose the acknowledged offset. First start after the switch:
//   - external file missing, legacy dir/state.json present: the legacy offset is copied over (never lost).
//   - both present: the LARGER offset wins (Telegram offsets only grow; the smaller one would replay updates).
//   - either file malformed: fail closed with an error, never start from 0 silently.
//   - neither present: fresh start (offset 0), as for a new install.
//
// The legacy file is never modified or deleted, so the old binary can still be pointed at it; after the external
// file has advanced the old file is stale and a rollback must copy the current external offset back first.
func resolveStatePaths(dir, extState, extDead string, log func(string)) (state, dead string, err error) {
	state, dead = filepath.Join(dir, "state.json"), filepath.Join(dir, "dead_letter.jsonl")
	if extState == "" && extDead == "" {
		return state, dead, nil
	}
	legacyState, legacyDead := state, dead
	if extState != "" {
		if !filepath.IsAbs(extState) {
			return "", "", fmt.Errorf("TG_STATE_FILE must be an absolute path")
		}
		state = filepath.Clean(extState)
		if state == legacyState {
			return state, dead, nil
		}
		if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
			return "", "", fmt.Errorf("cannot create the folder for TG_STATE_FILE: %v", err)
		}
		lo, lerr := loadOffset(legacyState) // malformed legacy => error, even if the external one exists
		if lerr != nil {
			return "", "", lerr
		}
		_, statErr := os.Stat(state)
		ext, eerr := loadOffset(state)
		if eerr != nil {
			return "", "", eerr
		}
		_, legacyStatErr := os.Stat(legacyState)
		switch {
		case os.IsNotExist(statErr) && legacyStatErr == nil:
			if err := saveOffset(state, lo); err != nil {
				return "", "", fmt.Errorf("cannot write the external state file: %v", err)
			}
			log(fmt.Sprintf("state: copied offset %d from %s to %s", lo, legacyState, state))
		case statErr == nil && legacyStatErr == nil && lo > ext:
			if err := saveOffset(state, lo); err != nil {
				return "", "", fmt.Errorf("cannot write the external state file: %v", err)
			}
			log(fmt.Sprintf("state: legacy offset %d was ahead of external %d; external updated", lo, ext))
		}
	}
	if extDead != "" {
		if !filepath.IsAbs(extDead) {
			return "", "", fmt.Errorf("TG_DEAD_LETTER_FILE must be an absolute path")
		}
		dead = filepath.Clean(extDead)
		if err := os.MkdirAll(filepath.Dir(dead), 0o700); err != nil {
			return "", "", fmt.Errorf("cannot create the folder for TG_DEAD_LETTER_FILE: %v", err)
		}
		if b, err := os.ReadFile(legacyDead); err == nil && dead != legacyDead {
			if _, e := os.Stat(dead); os.IsNotExist(e) {
				if err := os.WriteFile(dead, b, 0o600); err != nil {
					return "", "", fmt.Errorf("cannot copy the dead-letter log: %v", err)
				}
				log("state: copied the dead-letter log to " + dead)
			}
		}
	}
	return state, dead, nil
}

// showOffset prints the offset stored at the resolved state path, for a cutover done while the bot is stopped.
func showOffset(dir string) (int, error) {
	sp, _, err := resolveStatePaths(dir, os.Getenv("TG_STATE_FILE"), "", func(string) {})
	if err != nil {
		return 0, err
	}
	return loadOffset(sp)
}
