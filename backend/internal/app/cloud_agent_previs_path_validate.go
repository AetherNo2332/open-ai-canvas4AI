package app

import "fmt"

func cloudAgentPrevisValidateMotionPathPoints(raw any) ([]any, error) {
	points, pointsOK := raw.([]any)
	if !pointsOK || len(points) < 2 || len(points) > cloudAgentPrevisPathMaxPoints {
		return nil, BadAuthRequest("世界引擎 motionPath 点数无效")
	}
	for index, point := range points {
		value, ok := cloudAgentPrevisNumberVec3(point)
		if !ok {
			return nil, BadAuthRequest(fmt.Sprintf("世界引擎 motionPath 点 %d 无效", index))
		}
		if err := cloudAgentPrevisValidateVec3(value, "motionPath point"); err != nil {
			return nil, err
		}
	}
	return points, nil
}
