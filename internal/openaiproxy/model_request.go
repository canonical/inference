package openaiproxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
)

type parsedModelRequest struct {
	model    string
	bodySize int
	rewrite  func(string) error
}

type modelRequestError struct {
	status  int
	message string
	reason  string
	err     error
}

type multipartPart struct {
	header textproto.MIMEHeader
	name   string
	data   []byte
}

func parseModelRequest(r *http.Request) (*parsedModelRequest, *modelRequestError) {
	if r.Method == http.MethodGet || isWebSocketUpgrade(r) {
		query := r.URL.Query()
		model := query.Get("model")
		if model == "" {
			return nil, invalidModelRequest("model is missing", "Query parameters must contain a model.", nil)
		}
		return &parsedModelRequest{
			model: model,
			rewrite: func(replacement string) error {
				query.Set("model", replacement)
				r.URL.RawQuery = query.Encode()
				return nil
			},
		}, nil
	}

	body, requestErr := readModelRequestBody(r)
	if requestErr != nil {
		return nil, requestErr
	}

	contentType := r.Header.Get("Content-Type")
	mediaType := ""
	parameters := map[string]string{}
	if contentType != "" {
		var err error
		mediaType, parameters, err = mime.ParseMediaType(contentType)
		if err != nil {
			return nil, invalidModelRequest("Content-Type is invalid", "Request Content-Type is invalid.", err)
		}
	}

	switch {
	case mediaType == "", mediaType == "application/json", strings.HasSuffix(mediaType, "+json"):
		return parseJSONModelRequest(r, body)
	case mediaType == "multipart/form-data":
		return parseMultipartModelRequest(r, body, parameters["boundary"])
	case mediaType == "application/x-www-form-urlencoded":
		return parseFormModelRequest(r, body)
	default:
		return nil, invalidModelRequest(
			"Content-Type does not support model routing",
			"Request Content-Type does not support model routing.",
			nil,
		)
	}
}

func readModelRequestBody(r *http.Request) ([]byte, *modelRequestError) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxProxyRequestSize+1))
	if err != nil {
		return nil, invalidModelRequest("request body could not be read", "Unable to read request body.", err)
	}
	if len(body) > maxProxyRequestSize {
		return nil, &modelRequestError{
			status:  http.StatusRequestEntityTooLarge,
			message: "Request body is too large.",
			reason:  "request body is too large",
		}
	}
	return body, nil
}

func parseJSONModelRequest(r *http.Request, body []byte) (*parsedModelRequest, *modelRequestError) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, invalidModelRequest("request body is not a JSON object", "Request body must be a JSON object.", err)
	}
	rawModel, exists := payload["model"]
	if !exists {
		return nil, invalidModelRequest("model is missing", "Request body must contain a model.", nil)
	}
	var model string
	if err := json.Unmarshal(rawModel, &model); err != nil || model == "" {
		return nil, invalidModelRequest("model is not a non-empty string", "Model must be a non-empty string.", err)
	}

	return &parsedModelRequest{
		model:    model,
		bodySize: len(body),
		rewrite: func(replacement string) error {
			encodedModel, err := json.Marshal(replacement)
			if err != nil {
				return fmt.Errorf("encoding model: %w", err)
			}
			payload["model"] = encodedModel
			rewrittenBody, err := json.Marshal(payload)
			if err != nil {
				return fmt.Errorf("encoding JSON body: %w", err)
			}
			setRequestBody(r, rewrittenBody)
			return nil
		},
	}, nil
}

func parseFormModelRequest(r *http.Request, body []byte) (*parsedModelRequest, *modelRequestError) {
	form, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, invalidModelRequest("form body is invalid", "Request body must be a valid URL-encoded form.", err)
	}
	model := form.Get("model")
	if model == "" {
		return nil, invalidModelRequest("model is missing", "Request body must contain a model.", nil)
	}

	return &parsedModelRequest{
		model:    model,
		bodySize: len(body),
		rewrite: func(replacement string) error {
			form.Set("model", replacement)
			setRequestBody(r, []byte(form.Encode()))
			return nil
		},
	}, nil
}

func parseMultipartModelRequest(r *http.Request, body []byte, boundary string) (*parsedModelRequest, *modelRequestError) {
	if boundary == "" {
		return nil, invalidModelRequest("multipart boundary is missing", "Multipart boundary is missing.", nil)
	}

	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	var parts []multipartPart
	model := ""
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, invalidModelRequest("multipart body is invalid", "Request body must be valid multipart form data.", err)
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return nil, invalidModelRequest("multipart part could not be read", "Unable to read multipart request body.", err)
		}
		name := part.FormName()
		if name == "model" && model == "" {
			model = string(data)
		}
		parts = append(parts, multipartPart{header: cloneMIMEHeader(part.Header), name: name, data: data})
	}
	if model == "" {
		return nil, invalidModelRequest("model is missing", "Request body must contain a model.", nil)
	}

	return &parsedModelRequest{
		model:    model,
		bodySize: len(body),
		rewrite: func(replacement string) error {
			var rewritten bytes.Buffer
			writer := multipart.NewWriter(&rewritten)
			if err := writer.SetBoundary(boundary); err != nil {
				return fmt.Errorf("setting multipart boundary: %w", err)
			}
			for _, part := range parts {
				header := cloneMIMEHeader(part.header)
				data := part.data
				if part.name == "model" {
					header.Del("Content-Length")
					data = []byte(replacement)
				}
				output, err := writer.CreatePart(header)
				if err != nil {
					return fmt.Errorf("creating multipart part: %w", err)
				}
				if _, err := output.Write(data); err != nil {
					return fmt.Errorf("writing multipart part: %w", err)
				}
			}
			if err := writer.Close(); err != nil {
				return fmt.Errorf("closing multipart body: %w", err)
			}
			setRequestBody(r, rewritten.Bytes())
			return nil
		},
	}, nil
}

func invalidModelRequest(reason, message string, err error) *modelRequestError {
	return &modelRequestError{
		status:  http.StatusBadRequest,
		message: message,
		reason:  reason,
		err:     err,
	}
}

func setRequestBody(r *http.Request, body []byte) {
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.TransferEncoding = nil
	r.Header.Del("Transfer-Encoding")
	r.Header.Set("Content-Length", strconv.Itoa(len(body)))
}

func cloneMIMEHeader(header textproto.MIMEHeader) textproto.MIMEHeader {
	cloned := make(textproto.MIMEHeader, len(header))
	for key, values := range header {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}

func isWebSocketUpgrade(r *http.Request) bool {
	return headerContainsToken(r.Header.Values("Connection"), "upgrade") &&
		headerContainsToken(r.Header.Values("Upgrade"), "websocket")
}

func headerContainsToken(values []string, token string) bool {
	for _, value := range values {
		for part := range strings.SplitSeq(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}
