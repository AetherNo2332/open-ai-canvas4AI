package service

import "infinite-canvas/backend/internal/canvas"

type CanvasEditProof = canvas.CanvasEditProof
type CanvasEditLeaseGrant = canvas.CanvasEditLeaseGrant
type CanvasSyncStatus = canvas.CanvasSyncStatus

const CanvasEditProofHeader = canvas.CanvasEditProofHeader

var ParseCanvasEditProofHeader = canvas.ParseCanvasEditProofHeader
var CanvasEditLeaseAppError = canvas.CanvasEditLeaseAppError
