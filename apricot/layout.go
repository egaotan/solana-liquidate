package apricot

import "encoding/binary"

type PriceLayout struct {
	Price uint64
}

func (price *PriceLayout) unpack(data []byte) error {
	price.Price = binary.LittleEndian.Uint64(data)
	return nil
}

type PoolLayout struct {

}