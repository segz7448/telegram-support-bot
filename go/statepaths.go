package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// stateOpts are the startup safety switches (env, all optional).
type stateOpts struct {
	ExtState, ExtDead string // TG_STATE_FILE, TG_DEAD_LETTER_FILE: absolute paths outside the deployment folder
	AllowFresh        bool   // TG_STATE_ALLOW_FRESH=1: with an external path, no state at either place is a new install
	Require           bool   // TG_STATE_REQUIRE_EXISTING=1: the resolved state file must already exist
}

// resolveStatePaths picks where the Telegram offset (state.json) and the dead-letter log live.
// Default (nothing set): TGBOT_DIR/state.json and dead_letter.jsonl exactly as before.
// With TG_STATE_FILE: the offset lives outside the deployment folder. First start after the switch:
//   - external missing, legacy present: the legacy offset is copied (never lost).
//   - both present: the larger offset wins, ONLY if neither file names a different bot id.
//   - neither present: refused unless TG_STATE_ALLOW_FRESH=1 (a vanished production state is not a new install).
//   - any malformed file, or a file recorded for another bot id: fail closed.
//
// The legacy file is never modified or deleted. After the external file advances the legacy copy is stale: a rollback
// to an old binary must copy the current external offset back first, with the bot stopped.
func resolveStatePaths(dir string, o stateOpts, log func(string)) (state, dead string, err error) {
	state, dead = filepath.Join(dir, "state.json"), filepath.Join(dir, "dead_letter.jsonl")
	legacyState, legacyDead := state, dead
	if o.ExtState != "" {
		if !filepath.IsAbs(o.ExtState) {
			return "", "", fmt.Errorf("TG_STATE_FILE must be an absolute path")
		}
		state = filepath.Clean(o.ExtState)
	}
	if o.ExtDead != "" {
		if !filepath.IsAbs(o.ExtDead) {
			return "", "", fmt.Errorf("TG_DEAD_LETTER_FILE must be an absolute path")
		}
		dead = filepath.Clean(o.ExtDead)
	}
	check := func(id string, p string) error {
		if id != "" && stateBotID != "" && id != stateBotID {
			return fmt.Errorf("state file %s belongs to a different bot (id %s, this bot is %s); refusing to reuse it", p, id, stateBotID)
		}
		return nil
	}
	lo, lid, lex, lerr := readState(legacyState)
	if lerr != nil && (o.ExtState != "" || o.Require) {
		return "", "", lerr
	}
	if state == legacyState { // default or explicit same path
		if o.Require && !lex {
			return "", "", fmt.Errorf("no state file at %s and TG_STATE_REQUIRE_EXISTING is set; refusing to treat this as a new install", state)
		}
		if err := check(lid, state); err != nil {
			return "", "", err
		}
	} else {
		eo, eid, eex, eerr := readState(state)
		if eerr != nil {
			return "", "", eerr
		}
		if err := check(eid, state); err != nil {
			return "", "", err
		}
		if lex {
			if err := check(lid, legacyState); err != nil {
				return "", "", err
			}
		}
		if eid != "" && lid != "" && eid != lid {
			return "", "", fmt.Errorf("the two state files name different bots; refusing to merge")
		}
		switch {
		case !eex && !lex:
			if !o.AllowFresh || o.Require {
				return "", "", fmt.Errorf("no state at %s or %s; refusing to treat this as a new install (set TG_STATE_ALLOW_FRESH=1 only for a genuinely new bot)", state, legacyState)
			}
		case !eex && lex, eex && lex && lo > eo:
			if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
				return "", "", fmt.Errorf("cannot create the folder for TG_STATE_FILE: %v", err)
			}
			if err := saveOffset(state, lo); err != nil {
				return "", "", fmt.Errorf("cannot write the external state file: %v", err)
			}
			log(fmt.Sprintf("state: offset %d taken from %s into %s", lo, legacyState, state))
		}
		if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
			return "", "", fmt.Errorf("cannot create the folder for TG_STATE_FILE: %v", err)
		}
	}
	if dead != legacyDead {
		if err := os.MkdirAll(filepath.Dir(dead), 0o700); err != nil {
			return "", "", fmt.Errorf("cannot create the folder for TG_DEAD_LETTER_FILE: %v", err)
		}
		if b, err := os.ReadFile(legacyDead); err == nil {
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

// showOffset is strictly read-only: it never creates, copies or writes anything. It reports the external state
// file when TG_STATE_FILE is set and exists, else the legacy one, with where it came from.
func showOffset(dir, ext string) (off int, botID, from string, err error) {
	legacy := filepath.Join(dir, "state.json")
	if ext != "" {
		if !filepath.IsAbs(ext) {
			return 0, "", "", fmt.Errorf("TG_STATE_FILE must be an absolute path")
		}
		if o, id, ex, e := readState(filepath.Clean(ext)); e != nil {
			return 0, "", "", e
		} else if ex {
			return o, id, filepath.Clean(ext), nil
		}
	}
	o, id, ex, e := readState(legacy)
	if e != nil {
		return 0, "", "", e
	}
	if !ex {
		return 0, "", "", fmt.Errorf("no state file found")
	}
	return o, id, legacy, nil
}
