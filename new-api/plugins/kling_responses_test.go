package plugins_test

import (
	"testing"

	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKlingResponsesProtocol(t *testing.T) {
	testVideoResponsesProtocol(t, videoResponsesTestCase{
		pluginKey: "kling",
		model:     "kling-v2-master",
		requestBody: map[string]any{
			"model": "kling-v2-master",
			"input": []any{map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "input_text", "text": "camera orbit"},
				map[string]any{"type": "input_image", "image_url": "https://cdn.example/frame.png"},
			}}},
			"seconds": 10,
			"metadata": map[string]any{
				"mode": "pro",
			},
		},
		wantAction: "image_to_video",
		wantRequest: map[string]any{
			"model":    "kling-v2-master",
			"prompt":   "camera orbit",
			"image":    "https://cdn.example/frame.png",
			"duration": float64(10),
			"metadata": map[string]any{"mode": "pro"},
		},
		wantUsageKeys:  []string{"seconds", "units"},
		wantVendorName: "kling",
	})
}

func TestKlingCompletionUsageFacts(t *testing.T) {
	source, err := builtinplugins.Source("kling")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "kling"})
	require.NoError(t, err)

	testCases := []struct {
		name string
		body map[string]any
		want map[string]any
	}{
		{
			name: "units and duration are both retained",
			body: map[string]any{"data": map[string]any{
				"final_unit_deduction": "3.5",
				"task_result":          map[string]any{"videos": []any{map[string]any{"duration": "5"}}},
			}},
			want: map[string]any{"units": float64(3.5), "seconds": int64(5)},
		},
		{
			name: "duration remains when unit deduction is absent",
			body: map[string]any{"data": map[string]any{
				"task_result": map[string]any{"videos": []any{map[string]any{"duration": "10"}}},
			}},
			want: map[string]any{"seconds": int64(10)},
		},
		{
			name: "unit deduction remains when duration is absent",
			body: map[string]any{"data": map[string]any{"final_unit_deduction": "2"}},
			want: map[string]any{"units": int64(2)},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			value, callErr := plugin.Engine.Call(t.Context(), "extractUsageOnComplete", nil, map[string]any{}, testCase.body)
			require.NoError(t, callErr)
			assert.Equal(t, testCase.want, value)
		})
	}

	t.Run("missing or malformed facts do not invent usage", func(t *testing.T) {
		value, callErr := plugin.Engine.Call(t.Context(), "extractUsageOnComplete", nil, map[string]any{}, map[string]any{
			"data": map[string]any{
				"final_unit_deduction": "unknown",
				"task_result":          map[string]any{"videos": []any{map[string]any{"duration": "unknown"}}},
			},
		})
		require.NoError(t, callErr)
		assert.Nil(t, value)
	})
}
