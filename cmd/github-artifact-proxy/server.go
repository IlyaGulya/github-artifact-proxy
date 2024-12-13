package main

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/v60/github"
	"github.com/julienschmidt/httprouter"
	log "github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
)

const (
	targetLockTimeout = 30 * time.Second
)

type Server struct {
	*ServerConfig
	router *httprouter.Router

	m       sync.Mutex
	clients map[*Target]*github.Client
}

type ServerConfig struct {
	Config         *Config
	BasePath       string
	GithubCacheTTL time.Duration
}

func NewServer(cfg *ServerConfig) *Server {
	if !strings.HasPrefix(cfg.BasePath, "/") {
		cfg.BasePath = "/" + cfg.BasePath
	}
	s := Server{
		ServerConfig: cfg,
		clients:      make(map[*Target]*github.Client),
	}

	r := httprouter.New()
	r.GET(s.buildURLPath("/targets/:target/runs/:run/artifacts/:artifact/*filename"), s.handleTargetRequest)

	s.router = r
	return &s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

func (s *Server) handleTargetRequest(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	targetId := params.ByName("target")
	runName := params.ByName("run")
	artifactName := params.ByName("artifact")
	filename := strings.TrimPrefix(params.ByName("filename"), "/")
	logCtx := log.WithFields(log.Fields{
		"addr":     r.RemoteAddr,
		"path":     r.URL.Path,
		"target":   targetId,
		"artifact": artifactName,
		"filename": filename,
		"run":      runName,
	})
	logCtx.Info("handling request")

	target, ok := s.getTarget(targetId)
	if !ok {
		logCtx.Warn("target not found")
		httpError(w, http.StatusNotFound)
		return
	}

	lockCtx, cancel := context.WithTimeout(r.Context(), targetLockTimeout)
	defer cancel()
	if err := target.Lock(lockCtx); err != nil {
		logCtx.WithError(err).WithField("timeout", targetLockTimeout).Error("unable to acquire target lock")
		httpError(w, http.StatusNotFound)
		return
	}
	defer target.Unlock()

	client := s.getClient(target)

	var cachedRun *Run
	if cachedRun, ok = target.runCache[runName]; !ok || time.Since(target.runCache[runName].FetchTime) > s.GithubCacheTTL {
		var run *github.WorkflowRun
		if runName == "latest" {
			listOpts := github.ListWorkflowRunsOptions{}
			if target.LatestFilter != nil {
				if target.LatestFilter.Branch != nil {
					listOpts.Branch = *target.LatestFilter.Branch
				}
				if target.LatestFilter.Event != nil {
					listOpts.Event = *target.LatestFilter.Event
				}
				if target.LatestFilter.Status != nil {
					listOpts.Status = *target.LatestFilter.Status
				}
			}

			wfRes, ghRes, err := client.Actions.ListWorkflowRunsByFileName(r.Context(), target.Owner, target.Repo, target.Filename, &listOpts)
			if err != nil {
				if ghRes != nil && ghRes.StatusCode == http.StatusNotFound {
					logCtx.WithError(err).Warn("unable to obtain workflow runs")
					httpError(w, http.StatusNotFound)
					return
				}

				logCtx.WithError(err).Error("unable to obtain workflow runs")
				httpError(w, http.StatusInternalServerError)
				return
			}

			logCtx.WithFields(log.Fields{
				"workflow": target.Filename,
				"amount":   len(wfRes.WorkflowRuns),
			}).Info("retrieved workflow runs")

			if len(wfRes.WorkflowRuns) == 0 {
				logCtx.Warn("list of workflow runs is empty")
				httpError(w, http.StatusNotFound)
				return
			}

			// We assume that the first workflow run in the list is the latest one. Luckily this
			// appears to always be the case, because there seems to be no way to specify
			// a sorting preference.
			run = wfRes.WorkflowRuns[0]
		} else {
			runID, err := strconv.ParseInt(runName, 10, 64)
			if err != nil {
				logCtx.WithError(err).Warn("unable the parse run ID")
				httpError(w, http.StatusBadRequest)
				return
			}
			wfRun, ghRes, err := client.Actions.GetWorkflowRunByID(r.Context(), target.Owner, target.Repo, runID)
			if err != nil {
				if ghRes != nil && ghRes.StatusCode == http.StatusNotFound {
					logCtx.WithError(err).Warn("unable to obtain workflow run")
					httpError(w, http.StatusNotFound)
					return
				}

				logCtx.WithError(err).Error("unable to obtain workflow run")
				httpError(w, http.StatusInternalServerError)
				return
			}

			run = wfRun
		}

		afRes, _, err := client.Actions.ListWorkflowRunArtifacts(r.Context(), target.Owner, target.Repo, *run.ID, nil)
		if err != nil {
			logCtx.WithError(err).Error("unable to obtain artifact list")
			httpError(w, http.StatusInternalServerError)
			return
		}

		logCtx.WithFields(log.Fields{
			"workflow": target.Filename,
			"amount":   len(afRes.Artifacts),
		}).Info("retrieved workflow artifacts")

		cachedRun = &Run{
			ID:        *run.ID,
			Artifacts: afRes.Artifacts,
			FetchTime: time.Now(),
		}
		target.runCache[runName] = cachedRun
	}

	var artifact *github.Artifact
	for _, af := range cachedRun.Artifacts {
		if af.Name != nil && *af.Name == artifactName {
			artifact = af
			break
		}
	}

	if artifact == nil || artifact.ID == nil {
		logCtx.Warn("artifact not found")
		httpError(w, http.StatusNotFound)
		return
	}

	// Get download URL from GitHub
	url, _, err := client.Actions.DownloadArtifact(r.Context(), target.Owner, target.Repo, *artifact.ID, 3)
	if err != nil {
		logCtx.WithError(err).Error("unable to obtain artifact download url")
		httpError(w, http.StatusInternalServerError)
		return
	}

	// Redirect directly to GitHub's download URL
	logCtx.WithFields(log.Fields{
		"redirect_url": url.String(),
	}).Info("redirecting to GitHub artifact download URL")

	w.Header().Add("Cache-Control", "no-cache")
	http.Redirect(w, r, url.String(), http.StatusTemporaryRedirect)
}

func (s *Server) getClient(t *Target) *github.Client {
	s.m.Lock()
	defer s.m.Unlock()

	ghClient, ok := s.clients[t]
	if !ok {
		var client *http.Client
		if t.Token != nil {
			client = oauth2.NewClient(context.Background(), oauth2.StaticTokenSource(
				&oauth2.Token{AccessToken: s.Config.Tokens[*t.Token]},
			))
		} else {
			client = new(http.Client)
		}

		client.Timeout = 30 * time.Second

		ghClient = github.NewClient(client)
		s.clients[t] = ghClient
	}

	return ghClient
}

func (s *Server) getTarget(name string) (*Target, bool) {
	s.m.Lock()
	defer s.m.Unlock()

	target, ok := s.Config.Targets[name]
	return target, ok
}

func (s *Server) buildURLPath(part string) string {
	return path.Join(s.BasePath, part)
}

func httpError(w http.ResponseWriter, status int) {
	msg := fmt.Sprintf("%d %s", status, strings.ToLower(http.StatusText(status)))
	http.Error(w, msg, status)
}
