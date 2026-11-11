package app

func cloudAgentPrevisDefaultPosition(kind, primitive string, position []float64) []float64 {
	if kind == "actor" || primitive == "character" {
		return []float64{0, 0, 0}
	}
	return position
}

func cloudAgentPrevisObjectColor(items []map[string]any, kind string) string {
	defaultColor := "#8795a5"
	if kind == "actor" {
		// Assign a distinct colour based on current actor count so each new actor stands out
		actorCount := 0
		for _, obj := range items {
			if stringValue(obj["kind"]) == "actor" {
				actorCount++
			}
		}
		actorColors := []string{"#2f7de1", "#d84949", "#dfae3f", "#34a276", "#8b5cf6", "#e879f9"}
		defaultColor = actorColors[actorCount%len(actorColors)]
	}

	return defaultColor
}
