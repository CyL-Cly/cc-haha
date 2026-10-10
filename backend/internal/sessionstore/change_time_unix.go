//go:build !windows

package sessionstore

import (
	"fmt"
	"math/big"
	"os"
	"reflect"
)

func fileChangeTimeNS(_ *os.File, info os.FileInfo) (string, error) {
	value := reflect.Indirect(reflect.ValueOf(info.Sys()))
	for _, name := range []string{"Ctimespec", "Ctim"} {
		time := value.FieldByName(name)
		if time.Kind() != reflect.Struct {
			continue
		}
		sec, nsec := time.FieldByName("Sec"), time.FieldByName("Nsec")
		if !sec.CanInt() || !nsec.CanInt() {
			continue
		}
		total := new(big.Int).Mul(big.NewInt(sec.Int()), big.NewInt(1000000000))
		return total.Add(total, big.NewInt(nsec.Int())).String(), nil
	}
	return "", fmt.Errorf("native stat lacks change time")
}
