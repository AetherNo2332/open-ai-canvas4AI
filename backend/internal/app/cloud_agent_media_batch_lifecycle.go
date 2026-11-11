package app

func (s *Service) closeCloudAgentMediaBatches() {
	s.mediaBatchMu.Lock()
	s.mediaBatchClosed = true
	for _, cancel := range s.mediaBatchWaiters {
		cancel()
	}
	s.mediaBatchMu.Unlock()
	s.mediaBatchWg.Wait()
}
