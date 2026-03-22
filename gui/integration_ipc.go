package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tigrisdata/tigrisfs/core"
)

const (
	integrationTokenHeader = "X-TigrisFS-Token"
	integrationCommandPath = "/v1/command"
	integrationMountsPath  = "/v1/mounts"
	integrationPathStatus  = "/v1/path/status"
	integrationPathBatch   = "/v1/path/statuses"
	integrationHealthPath  = "/v1/health"
)

type integrationCommand struct {
	Action    string `json:"action"`
	Path      string `json:"path,omitempty"`
	Recursive bool   `json:"recursive,omitempty"`
}

type integrationResponse struct {
	Success bool            `json:"success"`
	Message string          `json:"message,omitempty"`
	Result  *core.PinResult `json:"result,omitempty"`
}

type integrationMountsResponse struct {
	Success bool                     `json:"success"`
	Mounts  []IntegrationMountStatus `json:"mounts,omitempty"`
	Message string                   `json:"message,omitempty"`
}

type integrationPathStatusRequest struct {
	Path string `json:"path"`
}

type integrationPathStatusResponse struct {
	Success bool                  `json:"success"`
	Status  IntegrationPathStatus `json:"status"`
	Message string                `json:"message,omitempty"`
}

type integrationPathStatusesRequest struct {
	Paths []string `json:"paths"`
}

type integrationPathStatusesResponse struct {
	Success  bool                    `json:"success"`
	Statuses []IntegrationPathStatus `json:"statuses,omitempty"`
	Message  string                  `json:"message,omitempty"`
}

type integrationHealthResponse struct {
	OK bool `json:"ok"`
}

type integrationServerState struct {
	Address   string    `json:"address"`
	Token     string    `json:"token"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
}

type integrationServer struct {
	server    *http.Server
	listener  net.Listener
	token     string
	stateFile string
	stopOnce  sync.Once
}

func integrationStateFile() string {
	return filepath.Join(configDir(), "integration_server.json")
}

func newIntegrationToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func executeIntegrationCommand(
	mgr *MountManager,
	onShow func(),
	cmd integrationCommand,
) integrationResponse {
	cmd.Action = strings.ToLower(strings.TrimSpace(cmd.Action))
	cmd.Path = strings.TrimSpace(cmd.Path)
	if (cmd.Action == "pin" || cmd.Action == "unpin" || cmd.Action == "unmount") && mgr == nil {
		return integrationResponse{Message: "mount manager is unavailable"}
	}

	switch cmd.Action {
	case "show":
		if onShow != nil {
			onShow()
		}
		return integrationResponse{
			Success: true,
			Message: "window focused",
		}
	case "pin":
		if cmd.Path == "" {
			return integrationResponse{Message: "path is required for pin action"}
		}
		result, err := mgr.PinAbsolutePath(cmd.Path, cmd.Recursive)
		if err != nil {
			return integrationResponse{Message: err.Error()}
		}
		return integrationResponse{
			Success: true,
			Message: fmt.Sprintf(
				"pinned %d file(s), %d dir(s), loaded %s",
				result.Files, result.Dirs, formatBytes(result.BytesRead),
			),
			Result: &result,
		}
	case "unpin":
		if cmd.Path == "" {
			return integrationResponse{Message: "path is required for unpin action"}
		}
		result, err := mgr.UnpinAbsolutePath(cmd.Path, cmd.Recursive)
		if err != nil {
			return integrationResponse{Message: err.Error()}
		}
		return integrationResponse{
			Success: true,
			Message: fmt.Sprintf(
				"unpinned %d file(s), %d dir(s), released %d buffer(s)",
				result.Files, result.Dirs, result.UnpinnedBuffer,
			),
			Result: &result,
		}
	case "unmount":
		if cmd.Path == "" {
			return integrationResponse{Message: "path is required for unmount action"}
		}
		if err := mgr.UnmountByAbsolutePath(cmd.Path); err != nil {
			return integrationResponse{Message: err.Error()}
		}
		return integrationResponse{
			Success: true,
			Message: "unmount initiated",
		}
	default:
		return integrationResponse{Message: "unsupported integration action"}
	}
}

func startIntegrationServer(mgr *MountManager, onShow func()) (*integrationServer, error) {
	token, err := newIntegrationToken()
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	s := &integrationServer{
		listener:  ln,
		token:     token,
		stateFile: integrationStateFile(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc(integrationHealthPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !integrationAuthorized(w, r, s.token) {
			return
		}
		writeIntegrationJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc(integrationCommandPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !integrationAuthorized(w, r, s.token) {
			return
		}
		defer r.Body.Close()

		var cmd integrationCommand
		if err := decodeIntegrationJSON(r.Body, &cmd); err != nil {
			writeIntegrationJSON(w, http.StatusBadRequest, integrationResponse{Message: "invalid request payload"})
			return
		}

		resp := executeIntegrationCommand(mgr, onShow, cmd)
		status := http.StatusOK
		if !resp.Success {
			status = http.StatusBadRequest
		}
		writeIntegrationJSON(w, status, resp)
	})
	mux.HandleFunc(integrationMountsPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !integrationAuthorized(w, r, s.token) {
			return
		}
		if mgr == nil {
			writeIntegrationJSON(w, http.StatusBadRequest, integrationMountsResponse{
				Success: false,
				Message: "mount manager is unavailable",
			})
			return
		}
		writeIntegrationJSON(w, http.StatusOK, integrationMountsResponse{
			Success: true,
			Mounts:  mgr.IntegrationMountSnapshot(),
		})
	})
	mux.HandleFunc(integrationPathStatus, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !integrationAuthorized(w, r, s.token) {
			return
		}
		if mgr == nil {
			writeIntegrationJSON(w, http.StatusBadRequest, integrationPathStatusResponse{
				Success: false,
				Message: "mount manager is unavailable",
			})
			return
		}

		path := strings.TrimSpace(r.URL.Query().Get("path"))
		if r.Method == http.MethodPost && path == "" {
			defer r.Body.Close()
			var req integrationPathStatusRequest
			if err := decodeIntegrationJSON(r.Body, &req); err != nil {
				writeIntegrationJSON(w, http.StatusBadRequest, integrationPathStatusResponse{
					Success: false,
					Message: "invalid request payload",
				})
				return
			}
			path = strings.TrimSpace(req.Path)
		}
		if path == "" {
			writeIntegrationJSON(w, http.StatusBadRequest, integrationPathStatusResponse{
				Success: false,
				Message: "path is required",
			})
			return
		}

		writeIntegrationJSON(w, http.StatusOK, integrationPathStatusResponse{
			Success: true,
			Status:  mgr.PathStatusAbsolute(path),
		})
	})
	mux.HandleFunc(integrationPathBatch, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !integrationAuthorized(w, r, s.token) {
			return
		}
		if mgr == nil {
			writeIntegrationJSON(w, http.StatusBadRequest, integrationPathStatusesResponse{
				Success: false,
				Message: "mount manager is unavailable",
			})
			return
		}

		defer r.Body.Close()
		var req integrationPathStatusesRequest
		if err := decodeIntegrationJSON(r.Body, &req); err != nil {
			writeIntegrationJSON(w, http.StatusBadRequest, integrationPathStatusesResponse{
				Success: false,
				Message: "invalid request payload",
			})
			return
		}
		writeIntegrationJSON(w, http.StatusOK, integrationPathStatusesResponse{
			Success:  true,
			Statuses: mgr.PathStatusesAbsolute(req.Paths),
		})
	})

	s.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	state := integrationServerState{
		Address:   ln.Addr().String(),
		Token:     token,
		PID:       os.Getpid(),
		StartedAt: time.Now(),
	}
	if err := writeIntegrationStateFile(s.stateFile, state); err != nil {
		_ = ln.Close()
		return nil, err
	}

	go func() {
		if err := s.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			guiLog.Errorf("Integration server failed: %v", err)
		}
	}()

	return s, nil
}

func (s *integrationServer) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if s.server != nil {
			_ = s.server.Shutdown(ctx)
		}
		if s.listener != nil {
			_ = s.listener.Close()
		}
		s.removeStateFileIfOwned()
	})
}

func (s *integrationServer) removeStateFileIfOwned() {
	state, err := readIntegrationStateFile(s.stateFile)
	if err != nil {
		return
	}
	if state.Token == s.token {
		_ = os.Remove(s.stateFile)
	}
}

func writeIntegrationStateFile(path string, state integrationServerState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readIntegrationStateFile(path string) (integrationServerState, error) {
	var state integrationServerState
	data, err := os.ReadFile(path)
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	if state.Address == "" || state.Token == "" {
		return state, fmt.Errorf("invalid integration server state")
	}
	return state, nil
}

func integrationAuthorized(w http.ResponseWriter, r *http.Request, token string) bool {
	if strings.TrimSpace(r.Header.Get(integrationTokenHeader)) == token {
		return true
	}
	w.WriteHeader(http.StatusUnauthorized)
	return false
}

func decodeIntegrationJSON(r io.Reader, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r, 1<<20))
	return decoder.Decode(target)
}

func writeIntegrationJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func sendIntegrationCommand(cmd integrationCommand) (integrationResponse, error) {
	var out integrationResponse
	if err := sendIntegrationJSONRequest(http.MethodPost, integrationCommandPath, cmd, &out); err != nil {
		return out, err
	}
	if !out.Success {
		if out.Message == "" {
			out.Message = "integration command failed"
		}
		return out, errors.New(out.Message)
	}
	return out, nil
}

func sendIntegrationPathStatus(path string) (IntegrationPathStatus, error) {
	var out integrationPathStatusResponse
	req := integrationPathStatusRequest{Path: path}
	if err := sendIntegrationJSONRequest(http.MethodPost, integrationPathStatus, req, &out); err != nil {
		return IntegrationPathStatus{}, err
	}
	if !out.Success {
		if out.Message == "" {
			out.Message = "integration path status failed"
		}
		return IntegrationPathStatus{}, errors.New(out.Message)
	}
	return out.Status, nil
}

func sendIntegrationPathStatuses(paths []string) ([]IntegrationPathStatus, error) {
	var out integrationPathStatusesResponse
	req := integrationPathStatusesRequest{Paths: paths}
	if err := sendIntegrationJSONRequest(http.MethodPost, integrationPathBatch, req, &out); err != nil {
		return nil, err
	}
	if !out.Success {
		if out.Message == "" {
			out.Message = "integration path status batch failed"
		}
		return nil, errors.New(out.Message)
	}
	return out.Statuses, nil
}

func sendIntegrationMounts() ([]IntegrationMountStatus, error) {
	var out integrationMountsResponse
	if err := sendIntegrationJSONRequest(http.MethodGet, integrationMountsPath, nil, &out); err != nil {
		return nil, err
	}
	if !out.Success {
		if out.Message == "" {
			out.Message = "integration mounts query failed"
		}
		return nil, errors.New(out.Message)
	}
	return out.Mounts, nil
}

func sendIntegrationHealth() error {
	var out integrationHealthResponse
	if err := sendIntegrationJSONRequest(http.MethodGet, integrationHealthPath, nil, &out); err != nil {
		return err
	}
	if !out.OK {
		return errors.New("integration health check failed")
	}
	return nil
}

func sendIntegrationJSONRequest(method, path string, payload any, out any) error {
	state, err := readIntegrationStateFile(integrationStateFile())
	if err != nil {
		return err
	}

	var body io.Reader
	if payload != nil {
		data, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return marshalErr
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, "http://"+state.Address+path, body)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(integrationTokenHeader, state.Token)

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if out != nil {
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
		if decodeErr != nil && !errors.Is(decodeErr, io.EOF) {
			return decodeErr
		}
	}
	if resp.StatusCode != http.StatusOK {
		switch typed := out.(type) {
		case *integrationResponse:
			if typed != nil && typed.Message != "" {
				return errors.New(typed.Message)
			}
		case *integrationMountsResponse:
			if typed != nil && typed.Message != "" {
				return errors.New(typed.Message)
			}
		case *integrationPathStatusResponse:
			if typed != nil && typed.Message != "" {
				return errors.New(typed.Message)
			}
		case *integrationPathStatusesResponse:
			if typed != nil && typed.Message != "" {
				return errors.New(typed.Message)
			}
		}
		return errors.New(resp.Status)
	}
	return nil
}
