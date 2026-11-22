package failurestore_test

import "errors"

func makeFIFO(string) error { return errors.New("Windows has no named pipes in the file system") }
