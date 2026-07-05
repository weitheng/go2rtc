package homekit

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/AlexxIT/go2rtc/internal/api"
	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/AlexxIT/go2rtc/pkg/mqtt"
)

// defaultMotionTimeout - auto off delay for the motion trigger API
// when active and timeout params are missing.
const defaultMotionTimeout = 30 * time.Second

// apiHomekitMotion - motion trigger API for HomeKit Secure Video:
//
//	POST /api/homekit/motion?src=camera1                  - motion pulse (auto off in 30s)
//	POST /api/homekit/motion?src=camera1&timeout=10       - motion pulse (auto off in 10s)
//	POST /api/homekit/motion?src=camera1&active=true      - motion on
//	POST /api/homekit/motion?src=camera1&active=false     - motion off
//
// Can be used as a webhook for any motion detection source
// (Home Assistant, Frigate, ONVIF events, etc.)
func apiHomekitMotion(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	src := r.Form.Get("src")
	srv := servers[src]
	if srv == nil {
		http.Error(w, api.StreamNotFound, http.StatusNotFound)
		return
	}

	var err error

	if s := r.Form.Get("active"); s != "" {
		if truthy(s) {
			err = srv.SetMotion(true)
		} else {
			err = srv.SetMotion(false)
		}
	} else {
		timeout := defaultMotionTimeout
		if n := core.Atoi(r.Form.Get("timeout")); n > 0 {
			timeout = time.Duration(n) * time.Second
		}
		err = srv.PulseMotion(timeout)
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	api.ResponseJSON(w, map[string]any{"src": src})
}

// motionMQTT subscribes to an MQTT topic with motion states, ex. Frigate:
//
//	homekit:
//	  camera1:
//	    secure_video: true
//	    motion_mqtt: mqtt://user:pass@192.168.1.2:1883/frigate/camera1/motion
//
// Payloads ON/OFF, true/false and 1/0 are supported.
func (s *server) motionMQTT(rawURL string) {
	u, err := url.Parse(rawURL)
	if err != nil {
		log.Error().Err(err).Msgf("[homekit] wrong motion_mqtt for %s", s.stream)
		return
	}

	address := u.Host
	if u.Port() == "" {
		address += ":1883"
	}

	topic := strings.TrimPrefix(u.Path, "/")
	if topic == "" {
		log.Error().Msgf("[homekit] missing topic in motion_mqtt for %s", s.stream)
		return
	}

	username := u.User.Username()
	password, _ := u.User.Password()

	for delay := time.Second; ; delay = min(delay*2, time.Minute) {
		if err = s.listenMQTT(address, topic, username, password); err != nil {
			log.Debug().Err(err).Msgf("[homekit] motion_mqtt for %s", s.stream)
		}

		time.Sleep(delay)
	}
}

func (s *server) listenMQTT(address, topic, username, password string) error {
	conn, err := net.DialTimeout("tcp", address, time.Second*5)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := mqtt.NewClient(conn)
	if err = client.Connect("go2rtc-"+core.RandString(8, 0), username, password); err != nil {
		return err
	}
	if err = client.Subscribe(topic); err != nil {
		return err
	}

	log.Debug().Msgf("[homekit] motion_mqtt connected for %s", s.stream)

	for {
		msgTopic, payload, err := client.Read()
		if err != nil {
			// quiet topic is fine - reset the deadline with a keepalive
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				if err = client.Ping(); err != nil {
					return err
				}
				continue
			}
			return err
		}
		if msgTopic != topic {
			continue
		}

		if err = s.SetMotion(truthy(string(payload))); err != nil {
			return err
		}
	}
}

func truthy(s string) bool {
	switch strings.ToLower(s) {
	case "on", "true", "1", "yes":
		return true
	}
	return false
}
