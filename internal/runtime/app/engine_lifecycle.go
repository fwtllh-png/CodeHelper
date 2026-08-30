package app

func closeEngine(engine Engine) error {
	closer, ok := engine.(interface{ Close() error })
	if !ok {
		return nil
	}
	return closer.Close()
}
