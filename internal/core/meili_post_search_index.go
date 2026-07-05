package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

type MeiliPostSearchIndexOptions struct {
	Endpoint     string
	APIKey       string
	Index        string
	Client       *http.Client
	TaskTimeout  time.Duration
	PollInterval time.Duration
}

type MeiliPostSearchIndex struct {
	endpoint     string
	apiKey       string
	index        string
	client       *http.Client
	taskTimeout  time.Duration
	pollInterval time.Duration
}

func NewMeiliPostSearchIndex(opts MeiliPostSearchIndexOptions) (*MeiliPostSearchIndex, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(opts.Endpoint), "/")
	if endpoint == "" {
		return nil, fmt.Errorf("meilisearch endpoint required")
	}
	index := strings.TrimSpace(opts.Index)
	if index == "" {
		index = "budgie_posts"
	}
	client := opts.Client
	if client == nil {
		client = http.DefaultClient
	}
	taskTimeout := opts.TaskTimeout
	if taskTimeout <= 0 {
		taskTimeout = 30 * time.Second
	}
	pollInterval := opts.PollInterval
	if pollInterval <= 0 {
		pollInterval = 200 * time.Millisecond
	}
	return &MeiliPostSearchIndex{
		endpoint:     endpoint,
		apiKey:       strings.TrimSpace(opts.APIKey),
		index:        index,
		client:       client,
		taskTimeout:  taskTimeout,
		pollInterval: pollInterval,
	}, nil
}

func (m *MeiliPostSearchIndex) UpsertPost(ctx context.Context, doc projections.PostSearchDocument) error {
	if strings.TrimSpace(doc.ID) == "" {
		doc.ID = doc.PostID
	}
	var task meiliTaskRef
	if err := m.doJSON(ctx, http.MethodPut, "/indexes/"+url.PathEscape(m.index)+"/documents", []projections.PostSearchDocument{doc}, &task); err != nil {
		return err
	}
	return m.waitTask(ctx, task.TaskUID())
}

func (m *MeiliPostSearchIndex) DeletePost(ctx context.Context, postID string) error {
	postID = strings.TrimSpace(postID)
	if postID == "" {
		return nil
	}
	var task meiliTaskRef
	if err := m.doJSON(ctx, http.MethodDelete, "/indexes/"+url.PathEscape(m.index)+"/documents/"+url.PathEscape(postID), nil, &task); err != nil {
		return err
	}
	return m.waitTask(ctx, task.TaskUID())
}

func (m *MeiliPostSearchIndex) Clear(ctx context.Context) error {
	var task meiliTaskRef
	if err := m.doJSON(ctx, http.MethodDelete, "/indexes/"+url.PathEscape(m.index)+"/documents", nil, &task); err != nil {
		return err
	}
	return m.waitTask(ctx, task.TaskUID())
}

func (m *MeiliPostSearchIndex) Search(ctx context.Context, query, boardID string, limit int) ([]string, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	body := meiliSearchRequest{
		Query: strings.TrimSpace(query),
		Limit: limit,
	}
	if strings.TrimSpace(boardID) != "" {
		body.Filter = `board_id = "` + strings.ReplaceAll(strings.TrimSpace(boardID), `"`, `\"`) + `"`
	}
	var response meiliSearchResponse
	if err := m.doJSON(ctx, http.MethodPost, "/indexes/"+url.PathEscape(m.index)+"/search", body, &response); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(response.Hits))
	for _, hit := range response.Hits {
		id := strings.TrimSpace(hit.ID)
		if id == "" {
			id = strings.TrimSpace(hit.PostID)
		}
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (m *MeiliPostSearchIndex) doJSON(ctx context.Context, method, path string, in, out any) error {
	if m == nil {
		return fmt.Errorf("nil meilisearch index")
	}
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, m.endpoint+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if m.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.apiKey)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("meilisearch %s %s: status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("meilisearch %s %s decode: %w", method, path, err)
	}
	return nil
}

func (m *MeiliPostSearchIndex) waitTask(ctx context.Context, taskUID int64) error {
	if taskUID <= 0 {
		return nil
	}
	taskCtx, cancel := context.WithTimeout(ctx, m.taskTimeout)
	defer cancel()
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()
	for {
		var task meiliTaskStatus
		if err := m.doJSON(taskCtx, http.MethodGet, "/tasks/"+strconv.FormatInt(taskUID, 10), nil, &task); err != nil {
			return err
		}
		switch strings.ToLower(task.Status) {
		case "succeeded":
			return nil
		case "failed", "canceled", "cancelled":
			if task.Error.Message != "" {
				return fmt.Errorf("meilisearch task %d %s: %s", taskUID, task.Status, task.Error.Message)
			}
			return fmt.Errorf("meilisearch task %d %s", taskUID, task.Status)
		}
		select {
		case <-taskCtx.Done():
			return taskCtx.Err()
		case <-ticker.C:
		}
	}
}

type meiliTaskRef struct {
	TaskUIDValue int64 `json:"taskUid"`
	UIDValue     int64 `json:"uid"`
}

func (t meiliTaskRef) TaskUID() int64 {
	if t.TaskUIDValue != 0 {
		return t.TaskUIDValue
	}
	return t.UIDValue
}

type meiliTaskStatus struct {
	Status string `json:"status"`
	Error  struct {
		Message string `json:"message"`
	} `json:"error"`
}

type meiliSearchRequest struct {
	Query  string `json:"q"`
	Filter string `json:"filter,omitempty"`
	Limit  int    `json:"limit"`
}

type meiliSearchResponse struct {
	Hits []struct {
		ID     string `json:"id"`
		PostID string `json:"post_id"`
	} `json:"hits"`
}
