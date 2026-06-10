package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/internal/model"
	"github.com/opencsgs/csghub-lite/pkg/api"
	"github.com/spf13/cobra"
)

func newImageCmd() *cobra.Command {
	var prompt string
	var negativePrompt string
	var output string
	var size string
	var count int
	var seed int
	var steps int
	var cfgScale float64
	var keepAlive string
	var inputPath string
	var extraInputs []string

	cmd := &cobra.Command{
		Use:   "image MODEL",
		Short: "Generate or edit an image with a local Diffusers model",
		Long: `Generate or edit image files with a local Diffusers model.

The command starts the local server if needed, pulls the model if it is missing,
loads the Diffusers image runtime, calls the OpenAI-compatible images API, and
writes PNG files to disk.

For image-to-image or editing models, pass one or more input images with --input.`,
		Example: `  csghub-lite image Qwen/Qwen-Image-2512 --prompt "a cute cat" --output cat.png
  csghub-lite image stabilityai/stable-diffusion-xl-base-1.0 --prompt "a mountain lake" --size 1024x1024 --steps 30
  csghub-lite image Qwen/Qwen-Image-Edit-2511 --prompt "make the sky sunset orange" --input photo.png --output edited.png`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := imageOptions{
				prompt:         prompt,
				negativePrompt: negativePrompt,
				output:         output,
				size:           size,
				count:          count,
				keepAlive:      keepAlive,
				inputPath:      inputPath,
				extraInputs:    extraInputs,
			}
			if cmd.Flags().Changed("seed") {
				opts.seed = &seed
			}
			if cmd.Flags().Changed("steps") {
				opts.steps = &steps
			}
			if cmd.Flags().Changed("cfg-scale") {
				opts.cfgScale = &cfgScale
			}
			return runImage(cmd, args[0], opts)
		},
	}

	cmd.Flags().StringVarP(&prompt, "prompt", "p", "", "image prompt")
	cmd.Flags().StringVar(&negativePrompt, "negative-prompt", "", "negative prompt")
	cmd.Flags().StringVarP(&output, "output", "o", "image.png", "output PNG path")
	cmd.Flags().StringVar(&size, "size", "1024x1024", "image size as WIDTHxHEIGHT")
	cmd.Flags().IntVarP(&count, "num", "n", 1, "number of images to generate (1-4)")
	cmd.Flags().IntVar(&seed, "seed", 0, "random seed")
	cmd.Flags().IntVar(&steps, "steps", 0, "number of inference steps")
	cmd.Flags().Float64Var(&cfgScale, "cfg-scale", 0, "classifier-free guidance scale")
	cmd.Flags().StringVar(&keepAlive, "keep-alive", "", "keep the model loaded after generation (for example 5m, 1h, or -1)")
	cmd.Flags().StringVar(&inputPath, "input", "", "input image path for image-to-image or editing models")
	cmd.Flags().StringArrayVar(&extraInputs, "input-extra", nil, "additional input image paths for multi-image editing models")
	return cmd
}

type imageOptions struct {
	prompt         string
	negativePrompt string
	output         string
	size           string
	count          int
	seed           *int
	steps          *int
	cfgScale       *float64
	keepAlive      string
	inputPath      string
	extraInputs    []string
}

func runImage(cmd *cobra.Command, modelID string, opts imageOptions) error {
	opts.prompt = strings.TrimSpace(opts.prompt)
	if opts.prompt == "" {
		return fmt.Errorf("--prompt is required")
	}
	if opts.count < 1 || opts.count > 4 {
		return fmt.Errorf("--num must be between 1 and 4")
	}
	if opts.output == "" {
		return fmt.Errorf("--output is required")
	}
	if _, _, err := api.ParseKeepAlive(opts.keepAlive); err != nil {
		return fmt.Errorf("--keep-alive %w", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	mgr := model.NewManager(cfg)
	if !mgr.Exists(modelID) {
		fmt.Printf("Model %s not found locally. Pulling from %s...\n", modelID, cfg.DisplayURL())
		if _, err := mgr.Pull(cmd.Context(), modelID, "", snapshotProgress()); err != nil {
			return fmt.Errorf("pull failed: %w", err)
		}
		fmt.Println("Pull complete.")
	}

	serverURL, err := ensureServer(cfg)
	if err != nil {
		return fmt.Errorf("starting server: %w", err)
	}
	fmt.Printf("Loading %s...\n", modelID)
	if err := preloadModel(serverURL, modelID, 0, 0, -1, "", "", "", opts.keepAlive); err != nil {
		return fmt.Errorf("loading model: %w", err)
	}

	req := api.OpenAIImagesGenerationRequest{
		Model:          modelID,
		Prompt:         opts.prompt,
		N:              &opts.count,
		Size:           opts.size,
		ResponseFormat: "b64_json",
		NegativePrompt: opts.negativePrompt,
		Source:         "cli",
	}
	if opts.seed != nil {
		req.Seed = opts.seed
	}
	if opts.steps != nil {
		req.Steps = opts.steps
	}
	if opts.cfgScale != nil {
		req.CFGScale = opts.cfgScale
	}
	inputImages, err := loadInputImagePaths(opts.inputPath, opts.extraInputs)
	if err != nil {
		return err
	}

	fmt.Printf("Generating %d image(s)...\n", opts.count)
	var resp *api.OpenAIImagesGenerationResponse
	if len(inputImages) > 0 {
		resp, err = editImages(cmd.Context(), serverURL, req, inputImages)
	} else {
		resp, err = generateImages(cmd.Context(), serverURL, req)
	}
	if err != nil {
		return err
	}
	if len(resp.Data) == 0 {
		return fmt.Errorf("image generation returned no images")
	}

	for i, image := range resp.Data {
		if image.B64JSON == "" {
			return fmt.Errorf("image %d did not include b64_json data", i+1)
		}
		data, err := base64.StdEncoding.DecodeString(image.B64JSON)
		if err != nil {
			return fmt.Errorf("decoding image %d: %w", i+1, err)
		}
		path := imageOutputPath(opts.output, i, len(resp.Data))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
			return fmt.Errorf("creating output directory: %w", err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
		fmt.Printf("Wrote %s\n", path)
	}
	return nil
}

func generateImages(ctx context.Context, serverURL string, req api.OpenAIImagesGenerationRequest) (*api.OpenAIImagesGenerationResponse, error) {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/v1/images/generations", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	return doImageRequest(ctx, httpReq)
}

func editImages(ctx context.Context, serverURL string, req api.OpenAIImagesGenerationRequest, inputPaths []string) (*api.OpenAIImagesGenerationResponse, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("model", req.Model)
	_ = writer.WriteField("prompt", req.Prompt)
	_ = writer.WriteField("source", req.Source)
	if req.N != nil {
		_ = writer.WriteField("n", strconv.Itoa(*req.N))
	}
	if strings.TrimSpace(req.Size) != "" {
		_ = writer.WriteField("size", req.Size)
	}
	if strings.TrimSpace(req.ResponseFormat) != "" {
		_ = writer.WriteField("response_format", req.ResponseFormat)
	}
	if strings.TrimSpace(req.NegativePrompt) != "" {
		_ = writer.WriteField("negative_prompt", req.NegativePrompt)
	}
	if req.Seed != nil {
		_ = writer.WriteField("seed", strconv.Itoa(*req.Seed))
	}
	if req.Steps != nil {
		_ = writer.WriteField("steps", strconv.Itoa(*req.Steps))
	}
	if req.CFGScale != nil {
		_ = writer.WriteField("cfg_scale", strconv.FormatFloat(*req.CFGScale, 'f', -1, 64))
	}
	for _, path := range inputPaths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading input image %s: %w", path, err)
		}
		part, err := writer.CreateFormFile("image", filepath.Base(path))
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/v1/images/edits", &body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())
	return doImageRequest(ctx, httpReq)
}

func doImageRequest(ctx context.Context, httpReq *http.Request) (*api.OpenAIImagesGenerationResponse, error) {
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(httpReq.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("image request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("image request failed: %s", strings.TrimSpace(string(errBody)))
	}
	var genResp api.OpenAIImagesGenerationResponse
	if err := json.NewDecoder(resp.Body).Decode(&genResp); err != nil {
		return nil, fmt.Errorf("decoding image response: %w", err)
	}
	return &genResp, nil
}

func imageOutputPath(output string, index, total int) string {
	if total <= 1 {
		return output
	}
	ext := filepath.Ext(output)
	stem := strings.TrimSuffix(output, ext)
	if ext == "" {
		ext = ".png"
	}
	return fmt.Sprintf("%s-%d%s", stem, index+1, ext)
}

func loadInputImagePaths(primary string, extras []string) ([]string, error) {
	paths := make([]string, 0, 1+len(extras))
	if strings.TrimSpace(primary) != "" {
		paths = append(paths, strings.TrimSpace(primary))
	}
	for _, path := range extras {
		path = strings.TrimSpace(path)
		if path != "" {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return nil, nil
	}
	return paths, nil
}
