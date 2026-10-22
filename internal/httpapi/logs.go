package httpapi

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"

	"k8s.io/apimachinery/pkg/util/validation"
)

type AppLogger interface {
	Logs(context.Context, string, string, int64) (io.ReadCloser, error)
}

func appLogs(logger AppLogger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			writeError(w, invalidRequest("Supply a valid URL query"))
			return
		}
		pod := query.Get("pod")
		tail := int64(100)
		if values, ok := query["tailLines"]; ok {
			if len(values) != 1 {
				writeError(w, invalidRequest("Supply one tailLines value"))
				return
			}
			tail, err = strconv.ParseInt(values[0], 10, 64)
		}
		if !appName.MatchString(name) || pod == "" || len(query["pod"]) != 1 || len(validation.IsDNS1123Subdomain(pod)) != 0 || err != nil || tail < 0 || tail > 1000 {
			writeError(w, invalidRequest("Supply a valid application name, one Pod name, and an integer tailLines from 0 to 1000"))
			return
		}
		body, err := logger.Logs(r.Context(), name, pod, tail)
		if err != nil {
			writeError(w, err)
			return
		}
		defer body.Close()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		controller := http.NewResponseController(w)
		if err := controller.Flush(); err != nil {
			log.Printf("log stream response flush failed app=%q pod=%q: %v", name, pod, err)
			return
		}
		buffer := make([]byte, 32*1024)
		for {
			n, readErr := body.Read(buffer)
			if n > 0 {
				if _, err := w.Write(buffer[:n]); err != nil {
					log.Printf("log stream response write failed app=%q pod=%q: %v", name, pod, err)
					return
				}
				if err := controller.Flush(); err != nil {
					log.Printf("log stream response flush failed app=%q pod=%q: %v", name, pod, err)
					return
				}
			}
			if readErr != nil {
				if readErr != io.EOF && r.Context().Err() == nil {
					log.Printf("log stream upstream read failed app=%q pod=%q: %v", name, pod, readErr)
				}
				return
			}
		}
	}
}
