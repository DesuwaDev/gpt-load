package geminiimage

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
)

const maxEditBodyBytes = 128 << 20

// ConvertEditRequest accepts uploaded images or JSON data URLs and preserves
// their order. Unsupported masks/resources are rejected rather than discarded.
func ConvertEditRequest(payload []byte, contentType string) ([]byte, error) {
	if len(payload) > maxEditBodyBytes {
		return nil, errors.New("Images edit body exceeds limit")
	}
	mediaType, parameters, err := mime.ParseMediaType(contentType)
	if err != nil && contentType != "" {
		return nil, errors.New("invalid Images Content-Type")
	}
	var fields map[string]json.RawMessage
	var images []imageData
	if strings.EqualFold(mediaType, "multipart/form-data") {
		if parameters["boundary"] == "" {
			return nil, errors.New("Images multipart boundary is required")
		}
		fields = make(map[string]json.RawMessage)
		reader := multipart.NewReader(bytes.NewReader(payload), parameters["boundary"])
		for count := 0; ; count++ {
			part, err := reader.NextRawPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, errors.New("invalid Images multipart body")
			}
			if count >= 128 {
				return nil, errors.New("Images multipart part count exceeds limit")
			}
			name := part.FormName()
			if encoding := part.Header.Get("Content-Transfer-Encoding"); encoding != "" && encoding != "binary" && encoding != "8bit" {
				return nil, unsupportedRequestError("Images edit does not support encoded multipart parts")
			}
			data, err := io.ReadAll(part)
			if err != nil {
				return nil, errors.New("cannot read Images multipart part")
			}
			if name == "image" || name == "image[]" {
				kind, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
				if kind == "" || kind == "application/octet-stream" {
					kind = http.DetectContentType(data)
				}
				if !supportedInputImageType(kind) || len(data) == 0 {
					return nil, errors.New("Images edit requires non-empty PNG, JPEG or WebP images")
				}
				images = append(images, imageData{MIMEType: kind, Data: base64.StdEncoding.EncodeToString(data)})
				continue
			}
			if name == "mask" {
				return nil, unsupportedRequestError("Gemini image edits do not support masks")
			}
			if part.FileName() != "" {
				return nil, unsupportedRequestError("unsupported Images edit file field")
			}
			if _, duplicate := fields[name]; duplicate {
				return nil, errors.New("Images edit fields must be unique")
			}
			if name == "n" || name == "stream" {
				fields[name] = json.RawMessage(bytes.TrimSpace(data))
			} else {
				fields[name], _ = json.Marshal(string(data))
			}
		}
	} else {
		if mediaType != "" && mediaType != "application/json" {
			return nil, errors.New("unsupported Images edit Content-Type")
		}
		if json.Unmarshal(payload, &fields) != nil || fields == nil {
			return nil, errors.New("Images edit requires a JSON object")
		}
		if _, ok := fields["mask"]; ok {
			return nil, unsupportedRequestError("Gemini image edits do not support masks")
		}
		imageInput, exists := fields["images"]
		if alternate, ok := fields["image"]; ok {
			if exists {
				return nil, errors.New("use either image or images")
			}
			imageInput = alternate
		}
		delete(fields, "image")
		delete(fields, "images")
		var items []json.RawMessage
		if len(imageInput) > 0 && bytes.TrimSpace(imageInput)[0] == '[' {
			if json.Unmarshal(imageInput, &items) != nil {
				return nil, errors.New("invalid Images edit images")
			}
		} else if len(imageInput) > 0 {
			items = []json.RawMessage{imageInput}
		}
		for _, item := range items {
			var url string
			if json.Unmarshal(item, &url) != nil {
				var object map[string]json.RawMessage
				if json.Unmarshal(item, &object) != nil || object == nil {
					return nil, errors.New("invalid Images edit image")
				}
				if len(object) != 1 || json.Unmarshal(object["image_url"], &url) != nil {
					return nil, unsupportedRequestError("Images edits require image_url data URLs, not file IDs")
				}
			}
			parsed, err := parseImageDataURL(url)
			if err != nil {
				return nil, err
			}
			images = append(images, parsed)
		}
	}
	if len(images) == 0 {
		return nil, errors.New("Images edits require at least one image")
	}
	if len(images) > 16 {
		return nil, unsupportedRequestError("Images edits support at most 16 input images")
	}
	controls, err := json.Marshal(fields)
	if err != nil {
		return nil, errors.New("invalid Images edit fields")
	}
	prompt, err := generationPrompt(controls)
	if err != nil {
		return nil, err
	}
	parts := []any{map[string]string{"text": prompt}}
	for _, image := range images {
		parts = append(parts, map[string]any{"inlineData": map[string]string{"mimeType": image.MIMEType, "data": image.Data}})
	}
	return json.Marshal(map[string]any{
		"contents":         []any{map[string]any{"role": "user", "parts": parts}},
		"generationConfig": map[string]any{"responseModalities": []string{"TEXT", "IMAGE"}},
	})
}

func supportedInputImageType(kind string) bool {
	return kind == "image/png" || kind == "image/jpeg" || kind == "image/webp"
}

func parseImageDataURL(value string) (imageData, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "data:") {
		return imageData{}, unsupportedRequestError("Gemini CLI image edits require uploaded images or base64 data URLs")
	}
	header, encoded, ok := strings.Cut(value[len("data:"):], ",")
	kind, encodedHeader := strings.CutSuffix(header, ";base64")
	if !ok || !encodedHeader || !supportedInputImageType(kind) || encoded == "" {
		return imageData{}, errors.New("invalid Images edit data URL")
	}
	size, err := io.Copy(io.Discard, base64.NewDecoder(base64.StdEncoding.Strict(), strings.NewReader(encoded)))
	if err != nil || size == 0 {
		return imageData{}, errors.New("invalid Images edit base64 data")
	}
	return imageData{MIMEType: kind, Data: encoded}, nil
}
