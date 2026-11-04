package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

// Shop hero image: an optional banner for the storefront, stored like an item photo (content-addressed under
// uploads/items, same size and type limits, served from /item-images/). The file name lives in
// shop.heroImage.file, which only these handlers write; a settings save updates only the alt text.

// heroImageResponse is what both hero endpoints return: the stored image, or nulls after a delete.
type heroImageResponse struct {
	File string `json:"file"`
	URL  string `json:"url"`
	Alt  string `json:"alt"`
}

func writeHeroImage(w http.ResponseWriter, h ShopHeroImage) {
	resp := heroImageResponse{File: h.File, Alt: h.Alt}
	if h.File != "" {
		resp.URL = "/item-images/" + h.File
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// setHeroImage replaces shop.heroImage in config.yaml under settingsWriteMu and returns the file it replaced.
func setHeroImage(configDir string, store *ConfigStore, hero ShopHeroImage) (old string, status int, err error) {
	settingsWriteMu.Lock()
	defer settingsWriteMu.Unlock()
	current := store.Get()
	desired := *current
	desired.Shop.HeroImage = hero
	builtin, err := defaultConfig()
	if err != nil {
		return "", http.StatusInternalServerError, err
	}
	if _, status, err := persistConfig(configDir, store, &desired, builtin); err != nil {
		return "", status, err
	}
	return current.Shop.HeroImage.File, http.StatusOK, nil
}

// removeHeroFileIfUnused deletes a replaced hero file unless an item photo or option photo still uses it.
func removeHeroFileIfUnused(db *sql.DB, configDir string, store *ConfigStore, file string) {
	if file == "" || store.Get().Shop.HeroImage.File == file {
		return
	}
	removeItemImageIfUnshared(db, configDir, file)
}

// handleHeroImageUpload is POST /api/shop/hero-image?alt=<text>: the raw image body, with the alt text in the
// query. Alt is required: the hero is content, not decoration. The old file is removed after the config
// change commits, unless an item still uses the same bytes.
func handleHeroImageUpload(configDir string, db *sql.DB, store *ConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		alt := strings.TrimSpace(r.URL.Query().Get("alt"))
		if alt == "" {
			writeAPIError(w, http.StatusUnprocessableEntity, "shop.heroImage.alt: required; describe the image for people who can't see it")
			return
		}
		if msg := plainTextProblem("shop.heroImage.alt", alt, shopHeroAltMaxChars); msg != "" {
			writeAPIError(w, http.StatusUnprocessableEntity, msg)
			return
		}
		name, ok := storeUploadedImage(configDir, w, r)
		if !ok {
			return
		}
		old, status, err := setHeroImage(configDir, store, ShopHeroImage{File: name, Alt: alt})
		if err != nil {
			log.Printf("shop hero image save: %v", err)
			removeHeroFileIfUnused(db, configDir, store, name)
			writeAPIError(w, status, "the hero image could not be saved")
			return
		}
		if old != "" && old != name {
			removeHeroFileIfUnused(db, configDir, store, old)
		}
		writeHeroImage(w, store.Get().Shop.HeroImage)
	}
}

// handleHeroImageDelete is DELETE /api/shop/hero-image: clears the hero image and its alt text. Deleting when
// there is none is a no-op success.
func handleHeroImageDelete(configDir string, db *sql.DB, store *ConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store.Get().Shop.HeroImage.File == "" {
			writeHeroImage(w, ShopHeroImage{})
			return
		}
		old, status, err := setHeroImage(configDir, store, ShopHeroImage{})
		if err != nil {
			log.Printf("shop hero image delete: %v", err)
			writeAPIError(w, status, "the hero image could not be removed")
			return
		}
		removeHeroFileIfUnused(db, configDir, store, old)
		writeHeroImage(w, ShopHeroImage{})
	}
}
