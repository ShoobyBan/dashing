package dashing

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"time"

	"sync"

	"gopkg.in/husobee/vestigo.v1"
	"gopkg.in/karlseguin/gerb.v0"
)

// A Server contains webservice parameters and middlewares.
type Server struct {
	dev     bool
	webroot string
	broker  *Broker
	mutex   sync.RWMutex
}

func param(r *http.Request, name string) string {
	return r.FormValue(fmt.Sprintf(":%s", name))
}

// IndexHandler redirects to the default dashboard.
func (s *Server) IndexHandler(w http.ResponseWriter, r *http.Request) {
	files, _ := filepath.Glob(s.webroot + "/dashboards/*.gerb")

	for _, file := range files {
		dashboard := strings.TrimSuffix(path.Base(file), path.Ext(file))
		if dashboard != "layout" {
			http.Redirect(w, r, fmt.Sprintf("/%s", dashboard), http.StatusTemporaryRedirect)
			return
		}
	}

	http.NotFound(w, r)
}

// EventsHandler opens a keepalive connection and pushes events to the client.
func (s *Server) EventsHandler(w http.ResponseWriter, r *http.Request) {
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}

	c, ok := w.(http.CloseNotifier)
	if !ok {
		http.Error(w, "Close notification unsupported!", http.StatusInternalServerError)
		return
	}

	// Create a new channel, over which the broker can
	// send this client events.
	events := make(chan *Event)

	// Add this client to the map of those that should
	// receive updates
	s.broker.newClients <- events

	// Remove this client from the map of attached clients
	// when the handler exits.
	defer func() {
		s.broker.defunctClients <- events
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	closer := c.CloseNotify()

	for {
		select {
		case event := <-events:
			s.mutex.Lock()
			data := event.Body
			data["id"] = event.ID
			data["updatedAt"] = int32(time.Now().Unix())
			json, err := json.Marshal(data)
			if err != nil {
				continue
			}
			s.mutex.Unlock()
			if event.Target != "" {
				fmt.Fprintf(w, "event: %s\n", event.Target)
			}
			fmt.Fprintf(w, "data: %s\n\n", json)
			f.Flush()
		case <-closer:
			return
		}
	}
}

// DashboardHandler serves the dashboard layout template.
func (s *Server) DashboardHandler(w http.ResponseWriter, r *http.Request) {
	dashboard := param(r, "dashboard")
	if dashboard == "" {
		dashboard = fmt.Sprintf("events%s", param(r, "suffix"))
	}
	path := fmt.Sprintf("%s/dashboards/%s.gerb", s.webroot, dashboard)
	log.Println(path)
	template, err := gerb.ParseFile(true, path, s.webroot+"/dashboards/layout.gerb")

	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=UTF-8")

	template.Render(w, map[string]interface{}{
		"dashboard":   dashboard,
		"development": s.dev,
		"request":     r,
	})
}

// DashboardEventHandler accepts dashboard events.
func (s *Server) DashboardEventHandler(w http.ResponseWriter, r *http.Request) {
	if r.Body != nil {
		defer r.Body.Close()
	}

	var data map[string]interface{}

	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	s.broker.events <- &Event{param(r, "id"), data, "dashboards"}

	w.WriteHeader(http.StatusNoContent)
}

// WidgetHandler serves widget templates.
func (s *Server) WidgetHandler(w http.ResponseWriter, r *http.Request) {
	widget := param(r, "widget")
	widget = widget[0 : len(widget)-5]
	path := fmt.Sprintf("%s/widgets/%s/%s.html", s.webroot, widget, widget)
	log.Println(path)
	template, err := gerb.ParseFile(true, path)

	if err != nil {
		log.Printf("%v", err)
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=UTF-8")

	template.Render(w, nil)
}

// WidgetEventHandler accepts widget data.
func (s *Server) WidgetEventHandler(w http.ResponseWriter, r *http.Request) {
	if r.Body != nil {
		defer r.Body.Close()
	}

	var data map[string]interface{}

	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		log.Printf("%v", err)
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	s.broker.events <- &Event{param(r, "id"), data, ""}

	w.WriteHeader(http.StatusNoContent)
}

// NewRouter creates a router with defaults.
func (s *Server) NewRouter(gets, posts map[string]http.HandlerFunc) *vestigo.Router {
	r := vestigo.NewRouter()
	r.Get("/", s.IndexHandler)
	r.Get("/events", s.EventsHandler)
	r.Get("/events:suffix", s.DashboardHandler) // workaround for router edge case
	r.Get("/:dashboard", s.DashboardHandler)
	r.Post("/dashboards/:id", s.DashboardEventHandler)
	r.Get("/views/:widget", s.WidgetHandler)
	r.Post("/widgets/:id", s.WidgetEventHandler)

	for route, handler := range gets {
		r.Get(route, handler)
	}

	for route, handler := range posts {
		r.Post(route, handler)
	}

	// Handle static files
	r.Get("/public/*", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, s.webroot+"/public/"+vestigo.Param(r, "_name"))
	})

	return r
}

// NewServer creates a Server instance.
func NewServer(b *Broker, webroot string) *Server {
	return &Server{
		dev:     false,
		webroot: webroot,
		broker:  b,
	}
}
