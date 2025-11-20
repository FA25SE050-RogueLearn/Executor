package response

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSON(t *testing.T) {
	t.Run("Successful JSON response", func(t *testing.T) {
		w := httptest.NewRecorder()
		data := map[string]string{"key": "value"}

		params := JSONResponseParameters{
			Status:  http.StatusOK,
			Data:    data,
			Success: true,
			Msg:     "Operation successful",
			ErrMsg:  "",
		}

		err := JSON(w, params)

		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

		var response JsonResponse
		err = json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.True(t, response.Success)
		assert.Equal(t, "Operation successful", response.Message)
		assert.Empty(t, response.ErrorMessage)
		assert.Nil(t, response.Data) // Data is nil when Success is true
	})

	t.Run("Error JSON response", func(t *testing.T) {
		w := httptest.NewRecorder()
		errorData := map[string]string{"error_details": "validation failed"}

		params := JSONResponseParameters{
			Status:  http.StatusBadRequest,
			Data:    errorData,
			Success: false,
			Msg:     "Request failed",
			ErrMsg:  "Invalid input parameters",
		}

		err := JSON(w, params)

		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

		var response JsonResponse
		err = json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.False(t, response.Success)
		assert.Equal(t, "Request failed", response.Message)
		assert.Equal(t, "Invalid input parameters", response.ErrorMessage)
		// Data is included when Success is false, but JSON unmarshaling converts it to map[string]interface{}
		dataMap, ok := response.Data.(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "validation failed", dataMap["error_details"])
	})

	t.Run("JSON response with nil data", func(t *testing.T) {
		w := httptest.NewRecorder()

		params := JSONResponseParameters{
			Status:  http.StatusCreated,
			Data:    nil,
			Success: true,
			Msg:     "Resource created",
			ErrMsg:  "",
		}

		err := JSON(w, params)

		require.NoError(t, err)
		assert.Equal(t, http.StatusCreated, w.Code)

		var response JsonResponse
		err = json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.True(t, response.Success)
		assert.Equal(t, "Resource created", response.Message)
		assert.Empty(t, response.ErrorMessage)
		assert.Nil(t, response.Data)
	})

	t.Run("JSON response with empty strings", func(t *testing.T) {
		w := httptest.NewRecorder()

		params := JSONResponseParameters{
			Status:  http.StatusNoContent,
			Data:    "",
			Success: true,
			Msg:     "",
			ErrMsg:  "",
		}

		err := JSON(w, params)

		require.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, w.Code)

		var response JsonResponse
		err = json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.True(t, response.Success)
		assert.Empty(t, response.Message)
		assert.Empty(t, response.ErrorMessage)
		assert.Nil(t, response.Data)
	})
}

func TestJSONWithHeaders(t *testing.T) {
	t.Run("JSON response with custom headers", func(t *testing.T) {
		w := httptest.NewRecorder()
		data := map[string]interface{}{
			"user_id": 123,
			"active":  true,
		}

		headers := make(http.Header)
		headers.Set("X-Custom-Header", "custom-value")
		headers.Set("X-Request-ID", "req-123")
		headers.Add("X-Multi-Header", "value1")
		headers.Add("X-Multi-Header", "value2")

		params := JSONResponseParameters{
			Status:  http.StatusOK,
			Data:    data,
			Success: true,
			Msg:     "Success with headers",
			ErrMsg:  "",
		}

		err := JSONWithHeaders(w, params, headers)

		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
		assert.Equal(t, "custom-value", w.Header().Get("X-Custom-Header"))
		assert.Equal(t, "req-123", w.Header().Get("X-Request-ID"))

		// Test multiple values for the same header
		multiValues := w.Header().Values("X-Multi-Header")
		assert.Len(t, multiValues, 2)
		assert.Contains(t, multiValues, "value1")
		assert.Contains(t, multiValues, "value2")

		var response JsonResponse
		err = json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.True(t, response.Success)
		assert.Equal(t, "Success with headers", response.Message)
		assert.Empty(t, response.ErrorMessage)
		assert.Nil(t, response.Data)
	})

	t.Run("JSON response with nil headers", func(t *testing.T) {
		w := httptest.NewRecorder()

		params := JSONResponseParameters{
			Status:  http.StatusOK,
			Data:    "test data",
			Success: true,
			Msg:     "Success without custom headers",
			ErrMsg:  "",
		}

		err := JSONWithHeaders(w, params, nil)

		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

		var response JsonResponse
		err = json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.True(t, response.Success)
		assert.Equal(t, "Success without custom headers", response.Message)
	})

	t.Run("JSON response with empty headers", func(t *testing.T) {
		w := httptest.NewRecorder()
		emptyHeaders := make(http.Header)

		params := JSONResponseParameters{
			Status:  http.StatusAccepted,
			Data:    nil,
			Success: true,
			Msg:     "Accepted",
			ErrMsg:  "",
		}

		err := JSONWithHeaders(w, params, emptyHeaders)

		require.NoError(t, err)
		assert.Equal(t, http.StatusAccepted, w.Code)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	})
}

func TestJSONResponseStructure(t *testing.T) {
	t.Run("Verify response structure for success case", func(t *testing.T) {
		w := httptest.NewRecorder()

		params := JSONResponseParameters{
			Status:  http.StatusOK,
			Data:    map[string]string{"test": "data"},
			Success: true,
			Msg:     "Success message",
			ErrMsg:  "This should be ignored",
		}

		err := JSON(w, params)
		require.NoError(t, err)

		var response JsonResponse
		err = json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		// For successful responses, Data should be nil
		assert.True(t, response.Success)
		assert.Equal(t, "Success message", response.Message)
		assert.Equal(t, "This should be ignored", response.ErrorMessage) // ErrorMessage is still set
		assert.Nil(t, response.Data)
	})

	t.Run("Verify response structure for error case", func(t *testing.T) {
		w := httptest.NewRecorder()
		errorData := map[string]interface{}{
			"field":  "username",
			"reason": "already exists",
		}

		params := JSONResponseParameters{
			Status:  http.StatusConflict,
			Data:    errorData,
			Success: false,
			Msg:     "Conflict occurred",
			ErrMsg:  "Username already exists",
		}

		err := JSON(w, params)
		require.NoError(t, err)

		var response JsonResponse
		err = json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		// For error responses, Data should contain the error data
		assert.False(t, response.Success)
		assert.Equal(t, "Conflict occurred", response.Message)
		assert.Equal(t, "Username already exists", response.ErrorMessage)
		assert.Equal(t, errorData, response.Data)
	})
}

func TestJSONResponseEdgeCases(t *testing.T) {
	t.Run("Response with complex data structure", func(t *testing.T) {
		w := httptest.NewRecorder()

		complexData := map[string]interface{}{
			"users": []map[string]interface{}{
				{"id": 1, "name": "Alice", "active": true},
				{"id": 2, "name": "Bob", "active": false},
			},
			"metadata": map[string]interface{}{
				"total":      2,
				"page":       1,
				"has_more":   false,
				"timestamps": []int64{1234567890, 1234567891},
			},
		}

		params := JSONResponseParameters{
			Status:  http.StatusOK,
			Data:    complexData,
			Success: false, // Set to false so Data is included
			Msg:     "Complex data response",
			ErrMsg:  "",
		}

		err := JSON(w, params)
		require.NoError(t, err)

		var response JsonResponse
		err = json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.False(t, response.Success)
		assert.NotNil(t, response.Data)

		// Verify the complex data structure is preserved
		dataMap, ok := response.Data.(map[string]interface{})
		require.True(t, ok)

		users, ok := dataMap["users"].([]interface{})
		require.True(t, ok)
		assert.Len(t, users, 2)

		metadata, ok := dataMap["metadata"].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, float64(2), metadata["total"]) // JSON numbers are float64
	})
}

func TestJSONResponseHeaders(t *testing.T) {
	t.Run("Verify Content-Type header is always set", func(t *testing.T) {
		w := httptest.NewRecorder()

		// Set a different Content-Type initially
		w.Header().Set("Content-Type", "text/plain")

		params := JSONResponseParameters{
			Status:  http.StatusOK,
			Data:    nil,
			Success: true,
			Msg:     "Test",
			ErrMsg:  "",
		}

		err := JSON(w, params)
		require.NoError(t, err)

		// Should be overridden to application/json
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	})

	t.Run("Custom headers don't override Content-Type", func(t *testing.T) {
		w := httptest.NewRecorder()

		headers := make(http.Header)
		headers.Set("Content-Type", "text/plain")
		headers.Set("X-Custom", "value")

		params := JSONResponseParameters{
			Status:  http.StatusOK,
			Data:    nil,
			Success: true,
			Msg:     "Test",
			ErrMsg:  "",
		}

		err := JSONWithHeaders(w, params, headers)
		require.NoError(t, err)

		// Content-Type should still be application/json (set after custom headers)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
		assert.Equal(t, "value", w.Header().Get("X-Custom"))
	})
}

func BenchmarkJSON(b *testing.B) {
	data := map[string]interface{}{
		"id":    123,
		"name":  "Test User",
		"email": "test@example.com",
		"data":  []string{"item1", "item2", "item3"},
	}

	params := JSONResponseParameters{
		Status:  http.StatusOK,
		Data:    data,
		Success: true,
		Msg:     "Success",
		ErrMsg:  "",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		_ = JSON(w, params)
	}
}

func BenchmarkJSONWithHeaders(b *testing.B) {
	data := map[string]interface{}{
		"id":   123,
		"name": "Test User",
	}

	headers := make(http.Header)
	headers.Set("X-Request-ID", "req-123")
	headers.Set("X-Version", "v1")

	params := JSONResponseParameters{
		Status:  http.StatusOK,
		Data:    data,
		Success: true,
		Msg:     "Success",
		ErrMsg:  "",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		_ = JSONWithHeaders(w, params, headers)
	}
}
