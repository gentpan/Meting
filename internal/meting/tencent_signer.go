package meting

import (
	_ "embed"
	"fmt"
	"sync"

	"github.com/dop251/goja"
)

//go:embed tencent_sign.js
var tencentSignModule string

type TencentSigner struct {
	mu sync.Mutex
	vm *goja.Runtime
	fn goja.Callable
}

func newTencentSigner() (*TencentSigner, error) {
	vm := goja.New()
	if _, err := vm.RunString(`var e = this; var window = this; var self = this; var console = { log: function(){} };`); err != nil {
		return nil, err
	}
	if _, err := vm.RunString(tencentSignModule); err != nil {
		return nil, err
	}
	callable, ok := goja.AssertFunction(vm.Get("ie"))
	if !ok {
		return nil, fmt.Errorf("tencent sign function not found")
	}
	return &TencentSigner{vm: vm, fn: callable}, nil
}

func (s *TencentSigner) Sign(payload string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	value, err := s.fn(goja.Undefined(), s.vm.ToValue(payload))
	if err != nil {
		return "", err
	}
	return value.String(), nil
}
