package models

import (
	"cmp"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"unicode"

	"github.com/opencode-ai/opencode/internal/logging"
	"github.com/spf13/viper"
)

const (
	ProviderLocal ModelProvider = "local"

	localModelsPath        = "v1/models"
	lmStudioBetaModelsPath = "api/v0/models"
)

func init() {
	if endpoint := os.Getenv("LOCAL_ENDPOINT"); endpoint != "" {
		logging.Info("LOCAL_ENDPOINT detected", "endpoint", endpoint)
		
		localEndpoint, err := url.Parse(endpoint)
		if err != nil {
			logging.Debug("Failed to parse local endpoint", "error", err, "endpoint", endpoint)
			// 파싱 실패시 하드코딩 모델 사용
			useHardcodedModel()
			return
		}

		load := func(url *url.URL, path string) []localModel {
			url.Path = path
			return listLocalModels(url.String())
		}

		models := load(localEndpoint, lmStudioBetaModelsPath)
		if len(models) == 0 {
			models = load(localEndpoint, localModelsPath)
		}

		if len(models) == 0 {
			logging.Debug("No local models found, using hardcoded model", "endpoint", endpoint)
			useHardcodedModel()
			return
		}

		loadLocalModels(models)
		viper.SetDefault("providers.local.apiKey", "dummy")
		ProviderPopularity[ProviderLocal] = 0
		logging.Info("Successfully loaded local models", "count", len(models))
	}
}

func useHardcodedModel() {
	// 자동 발견 실패시 하드코딩 모델 사용
	// 환경 변수에서 모델명 가져오기, 없으면 기본값 사용
	modelName := os.Getenv("LOCAL_MODEL_NAME")
	if modelName == "" {
		// 설정 파일에서 로컬 모델 이름 가져오기
		configModelName := viper.GetString("local.model")
		if configModelName != "" {
			modelName = configModelName
		} else {
			modelName = "Qwen3-32B"
		}
	}
	
	logging.Info("Using hardcoded local model", "model", modelName)
	fallbackModel := localModel{
		ID:                  modelName,
		Object:              "model",
		Type:                "llm",
		State:               "loaded",
		MaxContextLength:    32768,
		LoadedContextLength: 32768,
	}
	
	models := []localModel{fallbackModel}
	loadLocalModels(models)
	
	viper.SetDefault("providers.local.apiKey", "dummy")
	ProviderPopularity[ProviderLocal] = 0
}

type localModelList struct {
	Data []localModel `json:"data"`
}

type localModel struct {
	ID                  string `json:"id"`
	Object              string `json:"object"`
	Type                string `json:"type"`
	Publisher           string `json:"publisher"`
	Arch                string `json:"arch"`
	CompatibilityType   string `json:"compatibility_type"`
	Quantization        string `json:"quantization"`
	State               string `json:"state"`
	MaxContextLength    int64  `json:"max_context_length"`
	LoadedContextLength int64  `json:"loaded_context_length"`
}

func listLocalModels(modelsEndpoint string) []localModel {
	// HTTP 클라이언트 생성
	client := &http.Client{}
	req, err := http.NewRequest("GET", modelsEndpoint, nil)
	if err != nil {
		logging.Debug("Failed to create request", "error", err, "endpoint", modelsEndpoint)
		return []localModel{}
	}
	
	// 사내 API와 동일한 헤더 추가
	req.Header.Set("Content-Type", "application/json")
	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	
	res, err := client.Do(req)
	if err != nil {
		logging.Debug("Failed to list local models", "error", err, "endpoint", modelsEndpoint)
		return []localModel{}
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		logging.Debug("Failed to list local models", "status", res.StatusCode, "endpoint", modelsEndpoint)
		return []localModel{}
	}

	var modelList localModelList
	if err = json.NewDecoder(res.Body).Decode(&modelList); err != nil {
		logging.Debug("Failed to decode model list", "error", err, "endpoint", modelsEndpoint)
		return []localModel{}
	}

	var supportedModels []localModel
	for _, model := range modelList.Data {
		if strings.HasSuffix(modelsEndpoint, lmStudioBetaModelsPath) {
			if model.Object != "model" || model.Type != "llm" {
				logging.Debug("Skipping unsupported LMStudio model",
					"endpoint", modelsEndpoint,
					"id", model.ID,
					"object", model.Object,
					"type", model.Type,
				)
				continue
			}
		}
		supportedModels = append(supportedModels, model)
	}

	return supportedModels
}

func loadLocalModels(models []localModel) {
	for i, m := range models {
		model := convertLocalModel(m)
		SupportedModels[model.ID] = model

		if i == 0 || m.State == "loaded" {
			viper.SetDefault("agents.coder.model", model.ID)
			viper.SetDefault("agents.summarizer.model", model.ID)
			viper.SetDefault("agents.task.model", model.ID)
			viper.SetDefault("agents.title.model", model.ID)
		}
	}
}

func convertLocalModel(model localModel) Model {
	return Model{
		ID:                  ModelID("local." + model.ID),
		Name:                friendlyModelName(model.ID),
		Provider:            ProviderLocal,
		APIModel:            model.ID,
		ContextWindow:       cmp.Or(model.LoadedContextLength, 4096),
		DefaultMaxTokens:    cmp.Or(model.LoadedContextLength, 4096),
		CanReason:           true,
		SupportsAttachments: true,
	}
}

var modelInfoRegex = regexp.MustCompile(`(?i)^([a-z0-9]+)(?:[-_]?([rv]?\d[\.\d]*))?(?:[-_]?([a-z]+))?.*`)

func friendlyModelName(modelID string) string {
	mainID := modelID
	tag := ""

	if slash := strings.LastIndex(mainID, "/"); slash != -1 {
		mainID = mainID[slash+1:]
	}

	if at := strings.Index(modelID, "@"); at != -1 {
		mainID = modelID[:at]
		tag = modelID[at+1:]
	}

	match := modelInfoRegex.FindStringSubmatch(mainID)
	if match == nil {
		return modelID
	}

	capitalize := func(s string) string {
		if s == "" {
			return ""
		}
		runes := []rune(s)
		runes[0] = unicode.ToUpper(runes[0])
		return string(runes)
	}

	family := capitalize(match[1])
	version := ""
	label := ""

	if len(match) > 2 && match[2] != "" {
		version = strings.ToUpper(match[2])
	}

	if len(match) > 3 && match[3] != "" {
		label = capitalize(match[3])
	}

	var parts []string
	if family != "" {
		parts = append(parts, family)
	}
	if version != "" {
		parts = append(parts, version)
	}
	if label != "" {
		parts = append(parts, label)
	}
	if tag != "" {
		parts = append(parts, tag)
	}

	return strings.Join(parts, " ")
}
