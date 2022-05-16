package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
)

type Metric struct {
	uid   string
	name  string
	value float64
}

// data structure: {"labels":{"uid":"e8b3b332-59d9-4895-b190-a17182511799"},"name":"own_max_memory_usage","timestamp":"2022-01-30T16:15:06Z","value":0}
func (m *Metric) UnmarshalJSON(data []byte) error {
	var r map[string]interface{}
	err := json.Unmarshal(data, &r)
	if err != nil {
		return err
	}
	for k, v := range r {
		switch k {
		case "labels":
			inner := v.(map[string]interface{})
			if uid, ok := inner["uid"]; ok {
				m.uid = uid.(string)
			} else {
				return fmt.Errorf("there was no uid attached to the message")
			}
		case "name":
			m.name = v.(string)
		case "value":
			m.value = v.(float64)
		default:
			continue
		}
	}
	return nil
}

func (m *Metric) UploadMemory(ctx context.Context) error {
	newValue := m.value * math.Pow(10, -6) //cAdvisor returns memory usage in B, we store it in MB
	return insertMetric(ctx, "Metrics", "MemoryUsage", m.uid, newValue)
}

func (m *Metric) UploadCPU(ctx context.Context) error {
	newValue := m.value * math.Pow(10, 3) // cAdvisor returns CPU usage in cores, we store it in millicores
	return insertMetric(ctx, "Metrics", "CPUUsage", m.uid, newValue)
}
