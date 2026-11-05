package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"syscall"

	"gopkg.in/yaml.v3"
)

// settingsWriteMu serializes writes to config.yaml so one PUT's bak-then-write can't interleave with another.
var settingsWriteMu sync.Mutex

// settingsPutCurrentReadHook runs right after handleSettingsPut reads current := store.Get().
// Tests use it to prove that read happens only while settingsWriteMu is held. Always nil outside tests.
var settingsPutCurrentReadHook func()

// settingsMaxBytes bounds a PUT body: a full config.yaml is a few KiB, so 256 KiB leaves headroom.
const settingsMaxBytes = 256 * 1024

// defaultConfig decodes the embedded defaults into a fresh, normalized Config, as LoadConfig does. It never
// reflects config.yaml on disk.
func defaultConfig() (*Config, error) {
	cfg := &Config{}
	if err := decodeStrict(defaultsYAML, cfg); err != nil {
		return nil, fmt.Errorf("embedded defaults are invalid (bug): %w", err)
	}
	normalizeConfig(cfg)
	return cfg, nil
}

// pendingConfig is the config the process would load from disk right now.
// It differs from what's running only when config.yaml was hand-edited after the last PUT or startup.
func pendingConfig(configDir string) (*Config, error) {
	return LoadConfig(configDir)
}

func handleSettingsGet(configDir string, store *ConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")

		builtin, err := defaultConfig()
		if err != nil {
			// The embedded defaults are a build-time invariant; a failure means the binary is broken.
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		resp := struct {
			Config    *Config `json:"config"`
			Builtin   *Config `json:"builtin"`
			LoadError string  `json:"loadError,omitempty"`
		}{Builtin: builtin}

		// pendingConfig re-reads config.yaml on every GET; it diverges from the store only when the file was
		// hand-edited into an invalid state, the one case handled below.
		pending, err := pendingConfig(configDir)
		if err != nil {
			resp.Config = store.Get()
			log.Printf("settings: pending config invalid: %v", err)
			if identityFor(r).isAdmin {
				resp.LoadError = err.Error()
			} else {
				resp.LoadError = "The saved settings file is invalid. Ask an admin to fix it."
			}
		} else {
			resp.Config = pending
		}
		if !identityFor(r).isAdmin {
			resp.Config = redactConfigForEmployee(resp.Config)
		}

		if err := json.NewEncoder(w).Encode(resp); err != nil {
			// Response already partially written; nothing more to do.
			_ = err
		}
	}
}

// handleSettingsPut implements PUT /api/settings. isAdmin picks the unrestricted admin path or the scoped
// non-admin path (an employee never gets blanket config PUT). Turning users.multiUser off kills every live
// employee session (killAllEmployeeSessions) so their access stops immediately.
func handleSettingsPut(configDir string, store *ConfigStore, auth *authState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, settingsMaxBytes)
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
				writeAPIError(w, http.StatusRequestEntityTooLarge, "request body too large")
				return
			}
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		// shop decodes onto the built-in defaults, so a request that sends only some shop keys (a client that
		// predates the storefront settings sends just maxOrderQty) keeps the default for the rest instead of
		// reading as "everything off". The "shop" key itself must be present, like "site" below.
		builtinShop, err := defaultConfig()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		body := struct {
			Config *Config `json:"config"`
		}{Config: &Config{Shop: builtinShop.Shop}}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		if body.Config == nil {
			writeAPIError(w, http.StatusBadRequest, "config: required")
			return
		}
		// An absent "site" object would decode to the zero Site and turn the shop on the main port off, so the
		// PUT must send it.
		var probe struct {
			Config map[string]json.RawMessage `json:"config"`
		}
		_ = json.Unmarshal(raw, &probe)
		if len(probe.Config["site"]) == 0 || string(probe.Config["site"]) == "null" {
			writeAPIError(w, http.StatusUnprocessableEntity, "site: required")
			return
		}
		if len(probe.Config["shop"]) == 0 || string(probe.Config["shop"]) == "null" {
			writeAPIError(w, http.StatusUnprocessableEntity, "shop: required")
			return
		}
		desired := body.Config

		normalizeConfig(desired)
		// The hero image file is owned by the hero image endpoints: a settings save keeps the stored file and
		// takes only the alt text, so a stale form can't resurrect a deleted image or name an arbitrary file.
		desired.Shop.HeroImage.File = store.Get().Shop.HeroImage.File
		problems := validate(desired)
		if p := usersMultiUserRequiresAuthProblem(desired.Users.MultiUser, auth.enabled); p != "" {
			problems = append(problems, p)
		}
		if email := desired.Users.AccessAdminEmail; email != "" && auth.db != nil {
			if _, taken, err := getUserByEmail(auth.db, email); err != nil {
				log.Printf("settings: checking users.accessAdminEmail against users: %v", err)
				http.Error(w, "settings not writable", http.StatusInternalServerError)
				return
			} else if taken {
				problems = append(problems, fmt.Sprintf("users.accessAdminEmail: %q is already an employee's email", email))
			}
		}
		if len(problems) > 0 {
			writeAPIError(w, http.StatusUnprocessableEntity, problems...)
			return
		}

		// settingsWriteMu is taken before store.Get() because the scoped-permission check runs against current:
		// a concurrent admin PUT between read and write would make it use a stale config, letting an employee's
		// PUT silently revert a key they hold no permission for.
		settingsWriteMu.Lock()
		defer settingsWriteMu.Unlock()

		// current is the effective config before this request, the baseline for "changed keys" (not the
		// defaults, or a customized key like smtp would reject every unrelated PUT).
		current := store.Get()
		if settingsPutCurrentReadHook != nil {
			settingsPutCurrentReadHook()
		}
		if desired.Shop.HeroImage.File != current.Shop.HeroImage.File {
			// A hero upload or delete landed between the first read and the lock; re-check against the new file.
			desired.Shop.HeroImage.File = current.Shop.HeroImage.File
			if problems := validateShop(desired.Shop); len(problems) > 0 {
				writeAPIError(w, http.StatusUnprocessableEntity, problems...)
				return
			}
		}
		id := identityFor(r)
		if !id.isAdmin {
			// An employee's GET /api/settings omits the SMTP block and the integration URLs, so an editor
			// that sends the whole config back sends them (and users.accessAdminEmail) blank. A blank block reads as "not sent": keep the
			// stored one. Any other value still reaches the scoped check below and is refused.
			if desired.Smtp == (Smtp{}) {
				desired.Smtp = current.Smtp
			}
			if desired.IntegrationsUrls == (IntegrationsUrls{}) {
				desired.IntegrationsUrls = current.IntegrationsUrls
			}
			if desired.Users.AccessAdminEmail == "" {
				desired.Users.AccessAdminEmail = current.Users.AccessAdminEmail
			}
			perms, err := userPermissions(auth.db, id.username)
			if err != nil {
				http.Error(w, "permission check failed", http.StatusInternalServerError)
				return
			}
			problem, err := settingsScopedProblem(desired, current, perms)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if problem != "" {
				writeAuthError(w, http.StatusForbidden, problem)
				return
			}
		}

		builtin, err := defaultConfig()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if problems := deletionGuardProblems(desired, builtin); len(problems) > 0 {
			writeAPIError(w, http.StatusUnprocessableEntity, problems...)
			return
		}

		reloaded, status, err := persistConfig(configDir, store, desired, builtin)
		if err != nil {
			writeConfigPersistError(w, status, err)
			return
		}
		warnIfSmtpAuthMisconfigured(reloaded)

		// Turning users.multiUser off must stop employee access immediately, as a disabled, deleted, or reset
		// user's session kill does (users.go).
		if current.Users.MultiUser && !reloaded.Users.MultiUser {
			auth.killAllEmployeeSessions()
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		// {} keeps the response a JSON object for callers that decode into a struct.
		json.NewEncoder(w).Encode(struct{}{})
	}
}

// persistConfig writes desired as sparse overrides over builtin to config.yaml, then reloads it as startup
// does (defaults plus the overrides just written) and swaps the result into store, so the store holds what a
// restart would produce. Callers hold settingsWriteMu and have validated desired. On error, status is the
// HTTP status to answer with; a write failure keeps writeConfigFile's mapping (507 for a full disk).
func persistConfig(configDir string, store *ConfigStore, desired, builtin *Config) (reloaded *Config, status int, err error) {
	// A write failure is reported as a JSON API error, the others as plain text (see writeConfigPersistError).
	overrides, err := diffOverrides(desired, builtin)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	yamlBody, err := yaml.Marshal(overrides)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	content := append([]byte(settingsFileHeader), yamlBody...)

	if err := writeConfigFile(configDir, content); err != nil {
		return nil, writeConfigFileErrorStatus(err), fmt.Errorf("writing config.yaml: %w", err)
	}
	// content came from a desired config that passed validate() and was written, so a reload failure means
	// something external raced the write. Report it rather than keep serving the old config.
	reloaded, err = LoadConfig(configDir)
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("reloading config.yaml after save: %w", err)
	}
	store.Set(reloaded)
	return reloaded, http.StatusOK, nil
}

// writeConfigPersistError answers a persistConfig failure: a failed write is a JSON API error, anything else
// plain text, as handleSettingsPut always has.
func writeConfigPersistError(w http.ResponseWriter, status int, err error) {
	if msg := err.Error(); strings.HasPrefix(msg, "writing config.yaml") {
		writeAPIError(w, status, msg)
		return
	}
	http.Error(w, err.Error(), status)
}

const settingsFileHeader = `# printpaca configuration
# Written by the printpaca Settings page. Only values that differ from
# the built-in defaults are stored here; hand-edits are preserved as
# values but comments are not. The previous file is kept as
# config.yaml.bak. Changes take effect immediately.
`

// deletionGuardProblems rejects a PUT omitting a key defaultConfig() defines in feeSchedules/presets:
// the deep merge would restore the built-in entry on the next start, contradicting what the user submitted.
func deletionGuardProblems(desired, defaults *Config) []string {
	var problems []string
	for key := range defaults.FeeSchedules {
		if _, ok := desired.FeeSchedules[key]; !ok {
			problems = append(problems, fmt.Sprintf("feeSchedules.%s: built-in entries cannot be removed", key))
		}
	}
	for key := range defaults.Presets {
		if _, ok := desired.Presets[key]; !ok {
			problems = append(problems, fmt.Sprintf("presets.%s: built-in entries cannot be removed", key))
		}
	}
	return problems
}

// writeConfigFileErrorStatus returns 507 only for ENOSPC/EDQUOT, else 500 (EACCES, EROFS, .bak read errors),
// so proxies, alerts, and retry policies keyed on status don't chase free space for an unrelated cause.
func writeConfigFileErrorStatus(err error) int {
	if errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT) {
		return http.StatusInsufficientStorage
	}
	return http.StatusInternalServerError
}

// writeConfigFile backs up any config.yaml to config.yaml.bak, then atomically replaces it.
// Callers must hold settingsWriteMu.
func writeConfigFile(configDir string, content []byte) error {
	path := filepath.Join(configDir, "config.yaml")
	bakPath := path + ".bak"

	if existing, err := os.ReadFile(path); err == nil {
		if err := os.WriteFile(bakPath, existing, 0o644); err != nil {
			return fmt.Errorf("backing up config.yaml: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading existing config.yaml: %w", err)
	}

	tmp, err := os.CreateTemp(configDir, "config.yaml.*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename below succeeds

	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// diffOverrides computes the sparse config.yaml keys that, merged over defaults, reproduce desired.
// Both configs are round-tripped through YAML so the comparison uses config.yaml's own key names.
func diffOverrides(desired, defaults *Config) (map[string]any, error) {
	desiredMap, err := toYAMLMap(desired)
	if err != nil {
		return nil, err
	}
	defaultsMap, err := toYAMLMap(defaults)
	if err != nil {
		return nil, err
	}
	pruned := pruneEqual(desiredMap, defaultsMap)
	result, ok := pruned.(map[string]any)
	if !ok {
		// Config is a struct, so this always round-trips to a map.
		return map[string]any{}, nil
	}
	return result, nil
}

func toYAMLMap(cfg *Config) (map[string]any, error) {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// pruneEqual recurses into two maps keeping only differing keys (keys missing from defaults are kept whole).
// Non-map values are kept iff not DeepEqual to defaults; arrays are compared and kept wholesale.
func pruneEqual(desired, defaults any) any {
	desiredMap, desiredIsMap := desired.(map[string]any)
	defaultsMap, defaultsIsMap := defaults.(map[string]any)
	if !desiredIsMap || !defaultsIsMap {
		return desired
	}

	out := map[string]any{}
	for key, dv := range desiredMap {
		bv, ok := defaultsMap[key]
		if !ok {
			out[key] = dv
			continue
		}
		dvMap, dvIsMap := dv.(map[string]any)
		bvMap, bvIsMap := bv.(map[string]any)
		if dvIsMap && bvIsMap {
			sub := pruneEqual(dvMap, bvMap)
			if subMap, ok := sub.(map[string]any); ok && len(subMap) == 0 {
				continue
			}
			out[key] = sub
			continue
		}
		if !reflect.DeepEqual(dv, bv) {
			out[key] = dv
		}
	}
	return out
}

// Scoped settings PUT: an employee's PUT is allowed only when every top-level key it changes belongs to one
// of their granted tabs (settingsScopedKeyPermission). Admin PUTs are never subject to this.

// changedTopLevelKeys returns the top-level keys whose value in desired differs from current: what this
// request changes, not what differs from defaults, so a long-standing customization can't block an employee.
func changedTopLevelKeys(desired, current *Config) (map[string]bool, error) {
	desiredMap, err := toYAMLMap(desired)
	if err != nil {
		return nil, err
	}
	currentMap, err := toYAMLMap(current)
	if err != nil {
		return nil, err
	}
	diff, ok := pruneEqual(desiredMap, currentMap).(map[string]any)
	if !ok {
		return map[string]bool{}, nil
	}
	keys := make(map[string]bool, len(diff))
	for k := range diff {
		keys[k] = true
	}
	return keys, nil
}

// settingsScopedKeyPermission maps a changed top-level key to the one tab permission covering it.
// A key absent here (besides the special-cased "printers") is never grantable: default-deny.
var settingsScopedKeyPermission = map[string]string{
	"materials": "filament",
	"energy":    "electricity",
	"presets":   "printers",
}

// printersChangePermissions reports which permissions a printers change needs: "printers" for structural
// changes, "filament" for a filamentDiameterMm-only change (the filament permission covers it too).
// Printers are matched by id, not position, so reordering alone isn't a change.
func printersChangePermissions(desired, current []Printer) map[string]bool {
	need := map[string]bool{}
	if len(desired) != len(current) {
		need["printers"] = true
		return need
	}
	curByID := make(map[string]Printer, len(current))
	for _, p := range current {
		curByID[p.ID] = p
	}
	seen := make(map[string]bool, len(desired))
	for _, dp := range desired {
		cp, ok := curByID[dp.ID]
		if !ok {
			need["printers"] = true
			continue
		}
		seen[dp.ID] = true
		if dp == cp {
			continue
		}
		otherFieldsEqual := dp.Name == cp.Name && dp.Watts == cp.Watts &&
			dp.PurchaseCost == cp.PurchaseCost && dp.LifespanHours == cp.LifespanHours
		if otherFieldsEqual {
			need["filament"] = true
		} else {
			need["printers"] = true
		}
	}
	for id := range curByID {
		if !seen[id] {
			need["printers"] = true
		}
	}
	return need
}

// settingsScopedProblem checks that perms cover every top-level key changed between current and desired.
// Returns "" when allowed, else a 403 message naming every offending key.
func settingsScopedProblem(desired, current *Config, perms []string) (string, error) {
	changed, err := changedTopLevelKeys(desired, current)
	if err != nil {
		return "", err
	}
	var offending []string
	for key := range changed {
		if key == "printers" {
			for permKey := range printersChangePermissions(desired.Printers, current.Printers) {
				if !hasPermission(perms, permKey) {
					offending = append(offending, "printers")
					break
				}
			}
			continue
		}
		permKey, mapped := settingsScopedKeyPermission[key]
		if !mapped || !hasPermission(perms, permKey) {
			offending = append(offending, key)
		}
	}
	if len(offending) == 0 {
		return "", nil
	}
	sort.Strings(offending)
	return fmt.Sprintf("not permitted to change: %s", strings.Join(offending, ", ")), nil
}
