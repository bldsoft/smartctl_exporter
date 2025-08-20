package main

import (
	"encoding/json"
	"net/http"
)

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
			model := jsonData.Get("model_name").String()
			if model == "" {
				model = jsonData.Get("scsi_model_name").String()
			}
			size := uint64(jsonData.Get("user_capacity.bytes").Uint())
			vendorFields := []string{
				"vendor",
				"scsi_vendor",
				"vendor_id",
				"manufacturer",
				"model_family",
				"model_name",
				"scsi_model_name",
			}
			// Check vendor fields in order, use the first one that exists
			// This is to ensure compatibility with different smartctl versions and formats
			var vendor string
			for _, field := range vendorFields {
				if jsonData.Get(field).Exists() {
					vendor = jsonData.Get(field).String()
					break
				}
			}

			serial := jsonData.Get("serial_number").String()
			if serial == "" {
				serial = jsonData.Get("scsi_serial_number").String()
			}
			devices = append(devices, DeviceInfo{
				Label:  device.Label,
				Name:   device.Name,
				Type:   device.Type,
				Model:  model,
				Size:   size,
				Vendor: vendor,
				Serial: serial,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(devices)
	}
}
