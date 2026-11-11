package app

import "strconv"

func applyCloudAgentExtraMediaOptions(config map[string]any, a cloudAgentMediaArgs) {
	if a.ImageResolution != "" {
		config["imageResolution"] = a.ImageResolution
	}
	if a.ImageParameters != "" {
		config["imageParameters"] = a.ImageParameters
	}
	if a.VideoWatermark != nil {
		config["videoWatermark"] = strconv.FormatBool(*a.VideoWatermark)
	}
	if a.VideoArkPrivateAssetUpload != nil {
		config["videoArkPrivateAssetUpload"] = strconv.FormatBool(*a.VideoArkPrivateAssetUpload)
	}
}
