package main

import (
	"compress/gzip"
	"encoding/json"
	"flag"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type jsPoly struct {
	Points [][2]float64 `json:"p"`
	Kind   int          `json:"k"` // 0=land, 1=hole, 2=antarctica
}

type jsCountry struct {
	Name    string       `json:"name"`
	Points  [][2]float64 `json:"p"`
	IsHole  bool         `json:"hole,omitempty"`
	Western bool         `json:"w,omitempty"`
}

type mapData struct {
	Land      []jsPoly    `json:"land"`
	Countries []jsCountry `json:"countries"`
}

var westernSubregions = map[string]bool{
	"Western Europe":            true,
	"Northern Europe":           true,
	"Southern Europe":           true,
	"Northern America":          true,
	"Australia and New Zealand": true,
}

func round3(v float64) float64 {
	return math.Round(v*1000) / 1000
}

func convertPolys(polys []Polygon, classifyAntarctica bool) []jsPoly {
	out := make([]jsPoly, 0, len(polys))
	for _, p := range polys {
		pts := make([][2]float64, len(p.WorldPoints))
		for i, pt := range p.WorldPoints {
			pts[i] = [2]float64{round3(pt.X), round3(pt.Y)}
		}
		kind := 0
		if p.IsHole {
			kind = 1
		} else if classifyAntarctica && p.Bounds.MaxY < -60 {
			kind = 2
		}
		out = append(out, jsPoly{Points: pts, Kind: kind})
	}
	return out
}

// gzipResponseWriter wraps http.ResponseWriter to compress responses.
type gzipResponseWriter struct {
	io.Writer
	http.ResponseWriter
}

func (w gzipResponseWriter) Write(b []byte) (int, error) {
	return w.Writer.Write(b)
}

func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		defer gz.Close()

		next.ServeHTTP(gzipResponseWriter{Writer: gz, ResponseWriter: w}, r)
	})
}

func main() {
	dataDir := flag.String("data", ".", "base directory containing asset/ folder")
	flag.Parse()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if _, err := strconv.Atoi(port); err != nil {
		log.Fatalf("invalid PORT: %v", err)
	}
	addr := ":" + port

	assetPath := func(rel string) string {
		return filepath.Join(*dataDir, rel)
	}

	log.Printf("worldrender starting on port %s", port)
	log.Println("Loading shapefiles...")

	landPolys, err := LoadShapefile(assetPath("asset/geodata/ne_50m_land/ne_50m_land.shp"))
	if err != nil {
		log.Fatalf("failed to load land shapefile: %v", err)
	}

	countryPolys, err := LoadCountryShapefile(assetPath("asset/geodata/ne_50m_admin_0_countries/ne_50m_admin_0_countries.shp"))
	if err != nil {
		log.Fatalf("failed to load country shapefile: %v", err)
	}

	log.Printf("Successfully loaded %d land polys, %d country polys", len(landPolys), len(countryPolys))

	data := mapData{
		Land: convertPolys(landPolys, true),
	}

	for _, cp := range countryPolys {
		pts := make([][2]float64, len(cp.WorldPoints))
		for i, pt := range cp.WorldPoints {
			pts[i] = [2]float64{round3(pt.X), round3(pt.Y)}
		}
		data.Countries = append(data.Countries, jsCountry{
			Name:    cp.Name,
			Points:  pts,
			IsHole:  cp.IsHole,
			Western: westernSubregions[cp.Subregion],
		})
	}

	mapJSON, err := json.Marshal(data)
	if err != nil {
		log.Fatalf("failed to marshal map data: %v", err)
	}

	log.Printf("Map data size: %.2f MB (uncompressed)", float64(len(mapJSON))/1024/1024)

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		htmlBytes, err := os.ReadFile(assetPath("asset/index.html"))
		if err != nil {
			http.Error(w, "page unavailable", http.StatusInternalServerError)
			log.Printf("failed to read index.html: %v", err)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Write(htmlBytes)
	})

	mux.Handle("/api/map", gzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(mapJSON)
	})))

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("Server running at http://localhost%s", addr)
	log.Printf("Map endpoint: http://localhost%s/api/map", addr)
	log.Printf("Health check: http://localhost%s/health", addr)
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
