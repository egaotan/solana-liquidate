package pyth

import (
	"bytes"
	"encoding/binary"
	"github.com/gagliardetto/solana-go"
	"math"
)

var (
	ProductType = 2
	PriceType   = 3
)

var (
	BaseLayoutSize     = 16
	PriceFixLayoutSize = 240
)

type BaseLayout struct {
	Magic   uint32
	Version uint32
	Type    uint32
	Size    uint32
}

type ProductLayout struct {
	BaseLayout
	PriceAccountKey solana.PublicKey
	Product         map[string]string
}

type KeyedProduct struct {
	ProductLayout
	Key    solana.PublicKey
	Height uint64
}

func (p *ProductLayout) unpack(data []byte) error {
	index := 0
	buf := bytes.NewReader(data[index : index+BaseLayoutSize])
	err := binary.Read(buf, binary.LittleEndian, &p.BaseLayout)
	if err != nil {
		return err
	}
	index += BaseLayoutSize
	buf = bytes.NewReader(data[index : index+32])
	err = binary.Read(buf, binary.LittleEndian, &p.PriceAccountKey)
	if err != nil {
		return err
	}
	index += 32
	//
	p.Product = make(map[string]string)
	for index < int(p.Size) {
		//
		keyLen := data[index]
		index++
		key := ""
		keyBytes := make([]byte, keyLen)
		//utils.ReverseBytes(data[index:index+int(keyLen)], keyBytes)
		copy(keyBytes, data[index:index+int(keyLen)])
		key = string(keyBytes)
		index += int(keyLen)
		//
		valueLen := data[index]
		index++
		value := ""
		valueBytes := make([]byte, valueLen)
		//utils.ReverseBytes(data[index:index+int(valueLen)], valueBytes)
		copy(valueBytes, data[index:index+int(valueLen)])
		value = string(valueBytes)
		index += int(valueLen)
		p.Product[key] = value
	}
	return nil
}

type PriceInfoLayout struct {
	Price           float64
	Confidence      float64
	Status          uint32
	CorporateAction uint32
	PublishSlot     uint32
}

func (price *PriceInfoLayout) unpack(data []byte, exp int32) error {
	//
	var priceComponent int64
	buf := bytes.NewReader(data[0:8])
	err := binary.Read(buf, binary.LittleEndian, &priceComponent)
	if err != nil {
		return err
	}
	y := math.Pow(10, float64(exp))
	price.Price = float64(priceComponent) * y
	//
	var confidenceComponent int64
	buf = bytes.NewReader(data[8:16])
	err = binary.Read(buf, binary.LittleEndian, &confidenceComponent)
	if err != nil {
		return err
	}
	price.Confidence = float64(confidenceComponent) * y
	//
	var status uint32
	buf = bytes.NewReader(data[16:20])
	err = binary.Read(buf, binary.LittleEndian, &status)
	if err != nil {
		return err
	}
	price.Status = status
	//
	var corporateAction uint32
	buf = bytes.NewReader(data[20:24])
	err = binary.Read(buf, binary.LittleEndian, &corporateAction)
	if err != nil {
		return err
	}
	price.CorporateAction = corporateAction
	//
	var publishSlot uint32
	buf = bytes.NewReader(data[24:32])
	err = binary.Read(buf, binary.LittleEndian, &publishSlot)
	if err != nil {
		return err
	}
	price.PublishSlot = publishSlot
	return nil
}

type PriceComponent struct {
	Aggregate PriceInfoLayout
	Latest    PriceInfoLayout
}

type EmaLayout struct {
	Value       float64
	Numerator   int64
	Denominator int64
}

func (ema *EmaLayout) unpack(data []byte, exp int32) error {
	var valueComponent int64
	buf := bytes.NewReader(data[0:8])
	err := binary.Read(buf, binary.LittleEndian, &valueComponent)
	if err != nil {
		return err
	}
	y := math.Pow(10, float64(exp))
	ema.Value = float64(valueComponent) * y
	//
	var numerator int64
	buf = bytes.NewReader(data[8:16])
	err = binary.Read(buf, binary.LittleEndian, &numerator)
	if err != nil {
		return err
	}
	ema.Numerator = numerator
	//
	var denominator int64
	buf = bytes.NewReader(data[16:24])
	err = binary.Read(buf, binary.LittleEndian, &denominator)
	if err != nil {
		return err
	}
	ema.Denominator = denominator
	return nil
}

func Drv(drvComponent int64, exp int32) float64 {
	y := math.Pow(10, float64(exp))
	value := float64(drvComponent) * y
	return value
}

type PriceLayout struct {
	BaseLayout
	PriceType           uint32
	Exponent            int32
	NumComponentPrices  uint32
	NumQuoters          uint32
	LastSlot            uint64
	ValidSlot           uint64
	Twap                EmaLayout
	Twac                EmaLayout
	Drv1                float64
	MinPublishers       uint8
	Drv2                int8
	Drv3                int16
	Drv4                int32
	ProductAccountKey   solana.PublicKey
	NextPriceAccountKey solana.PublicKey
	PreviousSlot        uint64
	PreviousPrice       float64
	PreviousConfidence  float64
	Drv5                float64
	Aggregate           PriceInfoLayout
	PriceComponents     map[solana.PublicKey]PriceComponent
	Price float64
}

type KeyedPrice struct {
	PriceLayout
	Key    solana.PublicKey
	Height uint64
}

func (p *PriceLayout) unpack(data []byte) error {
	index := 0
	buf := bytes.NewReader(data[index : index+BaseLayoutSize])
	err := binary.Read(buf, binary.LittleEndian, &p.BaseLayout)
	if err != nil {
		return err
	}
	index += BaseLayoutSize
	p.PriceType = binary.LittleEndian.Uint32(data[index : index+4])
	index += 4
	p.Exponent = int32(binary.LittleEndian.Uint32(data[index : index+4]))
	index += 4
	p.NumComponentPrices = binary.LittleEndian.Uint32(data[index : index+4])
	index += 4
	p.NumQuoters = binary.LittleEndian.Uint32(data[index : index+4])
	index += 4
	p.LastSlot = binary.LittleEndian.Uint64(data[index : index+8])
	index += 8
	p.ValidSlot = binary.LittleEndian.Uint64(data[index : index+8])
	index += 8
	p.Twap.unpack(data[index:index+24], p.Exponent)
	index += 24
	p.Twac.unpack(data[index:index+24], p.Exponent)
	index += 24
	drv1Component := int64(binary.LittleEndian.Uint64(data[index : index+8]))
	index += 8
	p.Drv1 = Drv(drv1Component, p.Exponent)
	p.MinPublishers = data[index]
	index += 1
	p.Drv2 = int8(data[index])
	index += 1
	p.Drv3 = int16(binary.LittleEndian.Uint16(data[index : index+2]))
	index += 2
	p.Drv4 = int32(binary.LittleEndian.Uint32(data[index : index+4]))
	index += 4
	key := solana.PublicKey{}
	buf = bytes.NewReader(data[index : index+32])
	err = binary.Read(buf, binary.LittleEndian, &key)
	if err != nil {
		return err
	}
	index += 32
	p.ProductAccountKey = key
	buf = bytes.NewReader(data[index : index+32])
	err = binary.Read(buf, binary.LittleEndian, &key)
	if err != nil {
		return err
	}
	index += 32
	p.NextPriceAccountKey = key
	p.PreviousSlot = binary.LittleEndian.Uint64(data[index : index+8])
	index += 8
	previousPriceComponent := int64(binary.LittleEndian.Uint64(data[index : index+8]))
	index += 8
	p.PreviousPrice = Drv(previousPriceComponent, p.Exponent)
	previousConfidenceComponent := int64(binary.LittleEndian.Uint64(data[index : index+8]))
	index += 8
	p.PreviousConfidence = Drv(previousConfidenceComponent, p.Exponent)
	drv5Component := int64(binary.LittleEndian.Uint64(data[index : index+8]))
	index += 8
	p.Drv5 = Drv(drv5Component, p.Exponent)
	p.Aggregate.unpack(data[index:index+32], p.Exponent)
	index += 32
	//
	shouldContinue := true
	p.PriceComponents = make(map[solana.PublicKey]PriceComponent)
	for index < len(data) && shouldContinue {
		//
		buf = bytes.NewReader(data[index : index+32])
		err = binary.Read(buf, binary.LittleEndian, &key)
		if err != nil {
			return err
		}
		index += 32
		//
		if key.IsZero() {
			shouldContinue = false
			continue
		}
		//
		var componentAggregate PriceInfoLayout
		componentAggregate.unpack(data, p.Exponent)
		index += 32
		//
		var latest PriceInfoLayout
		latest.unpack(data, p.Exponent)
		index += 32
		p.PriceComponents[key] = PriceComponent{
			Aggregate: componentAggregate,
			Latest:    latest,
		}
	}
	//
	if p.Aggregate.Status == 1 {
		p.Price = p.Aggregate.Price
	}
	return nil
}
