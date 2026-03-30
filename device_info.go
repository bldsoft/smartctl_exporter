package main

import (
	"encoding/json"
	"net/http"

	"github.com/tidwall/gjson"
)

func getDeviceModel(device gjson.Result) string {
	model := device.Get("model_name").String()
	if model == "" {
		model = device.Get("scsi_model_name").String()
	}
	return model
}

func getDeviceVendor(device gjson.Result) string {
	// Check vendor fields in order, use the first one that exists
	// This is to ensure compatibility with different smartctl versions and formats
	var vendor string
	vendorFields := []string{
		"vendor",
		"scsi_vendor",
		"vendor_id",
		"manufacturer",
		"model_family",
		"model_name",
		"scsi_model_name",
	}
	for _, field := range vendorFields {
		if device.Get(field).Exists() {
			vendor = device.Get(field).String()
			break
		}
	}
	return vendor
}

// devicesHandler returns a http.HandlerFunc for /devices endpoint
func devicesHandler(collector *SMARTctlManagerCollector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		collector.mutex.Lock()
		defer collector.mutex.Unlock()

		type DeviceInfo struct {
			Label  string `json:"label"`
			Name   string `json:"name"`
			Type   string `json:"type"`
			Model  string `json:"model"`
			Size   uint64 `json:"size_bytes"`
			Vendor string `json:"vendor"`
			Serial string `json:"serial"`
		}

		devices := make([]DeviceInfo, 0, len(collector.Devices))
		for _, device := range collector.Devices {
			jsonData := readData(collector.logger, device)
			model := getDeviceModel(jsonData)
			vendor := getDeviceVendor(jsonData)
			size := uint64(jsonData.Get("user_capacity.bytes").Uint())
			if size == 0 {
				size = uint64(jsonData.Get("nvme_total_capacity").Uint())
			}

			devices = append(devices, DeviceInfo{
				Label:  device.Label,
				Name:   device.Name,
				Type:   device.Type,
				Model:  model,
				Size:   size,
				Vendor: vendor,
				Serial: device.Serial,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(devices)
	}
}
