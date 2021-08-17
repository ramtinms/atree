/*
 * Copyright 2021 Dapper Labs, Inc.  All rights reserved.
 */

package atree

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/fxamacker/cbor/v2"
)

const (
	basicArrayDataSlabPrefixSize = 1 + 8
)

type BasicArrayDataSlab struct {
	header   ArraySlabHeader
	elements []Storable
}

func (a *BasicArrayDataSlab) StoredValue(storage SlabStorage) (Value, error) {
	return &BasicArray{storage: storage, root: a}, nil
}

func (a *BasicArrayDataSlab) DeepRemove(storage SlabStorage) error {
	return storage.Remove(a.ID())
}

type BasicArray struct {
	storage SlabStorage
	root    *BasicArrayDataSlab
}

var _ Value = &BasicArray{}

func (a *BasicArray) DeepCopy(storage SlabStorage, address Address) (Value, error) {
	result := NewBasicArray(storage, address)

	for i, element := range a.root.elements {
		value, err := element.StoredValue(storage)
		if err != nil {
			return nil, err
		}

		valueCopy, err := value.DeepCopy(storage, address)
		if err != nil {
			return nil, err
		}

		err = result.Insert(uint64(i), valueCopy)
		if err != nil {
			return nil, err
		}
	}

	return result, nil
}

func (a *BasicArray) DeepRemove(storage SlabStorage) error {
	count := a.Count()

	// TODO: use backward iterator
	for prevIndex := count; prevIndex > 0; prevIndex-- {
		index := prevIndex - 1

		storable, err := a.root.Get(storage, index)
		if err != nil {
			return err
		}

		value, err := a.Remove(index)
		if err != nil {
			return err
		}

		err = value.DeepRemove(storage)
		if err != nil {
			return err
		}

		err = storable.DeepRemove(storage)
		if err != nil {
			return err
		}
	}

	return a.root.DeepRemove(storage)
}

func (a *BasicArray) Storable(_ SlabStorage, _ Address) (Storable, error) {
	return a.root, nil
}

func NewBasicArrayDataSlab(storage SlabStorage, address Address) *BasicArrayDataSlab {
	sid, err := storage.GenerateStorageID(address)
	if err != nil {
		panic(err)
	}
	return &BasicArrayDataSlab{
		header: ArraySlabHeader{
			id:   sid,
			size: basicArrayDataSlabPrefixSize,
		},
	}
}

func newBasicArrayDataSlabFromData(
	id StorageID,
	data []byte,
	decMode cbor.DecMode,
	decodeStorable StorableDecoder,
) (
	*BasicArrayDataSlab,
	error,
) {
	if len(data) < 2 {
		return nil, errors.New("data is too short for basic array")
	}

	// Check flag
	if getSlabArrayType(data[1]) != slabBasicArray {
		return nil, fmt.Errorf("data has invalid flag 0x%x, want 0x%x", data[0], maskBasicArray)
	}

	cborDec := decMode.NewByteStreamDecoder(data[2:])

	elemCount, err := cborDec.DecodeArrayHead()
	if err != nil {
		return nil, err
	}

	elements := make([]Storable, elemCount)
	for i := 0; i < int(elemCount); i++ {
		storable, err := decodeStorable(cborDec, StorageIDUndefined)
		if err != nil {
			return nil, err
		}
		elements[i] = storable
	}

	return &BasicArrayDataSlab{
		header:   ArraySlabHeader{id: id, size: uint32(len(data)), count: uint32(elemCount)},
		elements: elements,
	}, nil
}

func (a *BasicArrayDataSlab) Encode(enc *Encoder) error {

	flag := maskBasicArray | maskSlabRoot

	// Encode flag
	_, err := enc.Write([]byte{0x0, flag})
	if err != nil {
		return err
	}

	// Encode CBOR array size for 9 bytes
	enc.Scratch[0] = 0x80 | 27
	binary.BigEndian.PutUint64(enc.Scratch[1:], uint64(len(a.elements)))

	_, err = enc.Write(enc.Scratch[:9])
	if err != nil {
		return err
	}

	for i := 0; i < len(a.elements); i++ {
		err := a.elements[i].Encode(enc)
		if err != nil {
			return err
		}
	}
	err = enc.CBOR.Flush()
	if err != nil {
		return err
	}

	return nil
}

func (a *BasicArrayDataSlab) Get(_ SlabStorage, index uint64) (Storable, error) {
	if index >= uint64(len(a.elements)) {
		return nil, fmt.Errorf("out of bounds")
	}
	v := a.elements[index]
	return v, nil
}

func (a *BasicArrayDataSlab) Set(storage SlabStorage, index uint64, v Storable) error {
	if index >= uint64(len(a.elements)) {
		return fmt.Errorf("out of bounds")
	}

	oldElem := a.elements[index]

	a.elements[index] = v

	a.header.size = a.header.size -
		oldElem.ByteSize() +
		v.ByteSize()

	err := storage.Store(a.header.id, a)
	if err != nil {
		return err
	}

	return nil
}

func (a *BasicArrayDataSlab) Insert(storage SlabStorage, index uint64, v Storable) error {
	if index > uint64(len(a.elements)) {
		return fmt.Errorf("out of bounds")
	}

	if index == uint64(len(a.elements)) {
		a.elements = append(a.elements, v)
	} else {
		a.elements = append(a.elements, nil)
		copy(a.elements[index+1:], a.elements[index:])
		a.elements[index] = v
	}

	a.header.count++
	a.header.size += v.ByteSize()

	err := storage.Store(a.header.id, a)
	if err != nil {
		return err
	}

	return nil
}

func (a *BasicArrayDataSlab) Remove(storage SlabStorage, index uint64) (Storable, error) {
	if index >= uint64(len(a.elements)) {
		return nil, fmt.Errorf("out of bounds")
	}

	v := a.elements[index]

	switch index {
	case 0:
		a.elements = a.elements[1:]
	case uint64(len(a.elements)) - 1:
		a.elements = a.elements[:len(a.elements)-1]
	default:
		copy(a.elements[index:], a.elements[index+1:])
		a.elements = a.elements[:len(a.elements)-1]
	}

	a.header.count--
	a.header.size -= v.ByteSize()

	err := storage.Store(a.header.id, a)
	if err != nil {
		return nil, err
	}

	return v, nil
}

func (a *BasicArrayDataSlab) Count() uint64 {
	return uint64(len(a.elements))
}

func (a *BasicArrayDataSlab) Header() ArraySlabHeader {
	return a.header
}

func (a *BasicArrayDataSlab) ByteSize() uint32 {
	return a.header.size
}

func (a *BasicArrayDataSlab) ID() StorageID {
	return a.header.id
}

func (a *BasicArrayDataSlab) String() string {
	return fmt.Sprintf("%v", a.elements)
}

func (a *BasicArrayDataSlab) Split(_ SlabStorage) (Slab, Slab, error) {
	return nil, nil, errors.New("not applicable")
}

func (a *BasicArrayDataSlab) Merge(_ Slab) error {
	return errors.New("not applicable")
}

func (a *BasicArrayDataSlab) LendToRight(_ Slab) error {
	return errors.New("not applicable")
}

func (a *BasicArrayDataSlab) BorrowFromRight(_ Slab) error {
	return errors.New("not applicable")
}

func NewBasicArray(storage SlabStorage, address Address) *BasicArray {
	return &BasicArray{
		storage: storage,
		root:    NewBasicArrayDataSlab(storage, address),
	}
}

func (a *BasicArray) StorageID() StorageID {
	return a.root.ID()
}

func (a *BasicArray) Address() Address {
	return a.StorageID().Address
}

func NewBasicArrayWithRootID(storage SlabStorage, id StorageID) (*BasicArray, error) {
	if id == StorageIDUndefined {
		return nil, fmt.Errorf("invalid storage id")
	}
	slab, found, err := storage.Retrieve(id)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("slab %d not found", id)
	}
	dataSlab, ok := slab.(*BasicArrayDataSlab)
	if !ok {
		return nil, fmt.Errorf("slab %d is not BasicArrayDataSlab", id)
	}
	return &BasicArray{storage: storage, root: dataSlab}, nil
}

func (a *BasicArray) Get(index uint64) (Value, error) {
	storable, err := a.root.Get(a.storage, index)
	if err != nil {
		return nil, err
	}
	return storable.StoredValue(a.storage)
}

func (a *BasicArray) Set(index uint64, v Value) error {
	storable, err := v.Storable(a.storage, a.Address())
	if err != nil {
		return err
	}
	return a.root.Set(a.storage, index, storable)
}

func (a *BasicArray) Append(v Value) error {
	index := uint64(a.root.header.count)
	return a.Insert(index, v)
}

func (a *BasicArray) Insert(index uint64, v Value) error {
	storable, err := v.Storable(a.storage, a.Address())
	if err != nil {
		return err
	}
	return a.root.Insert(a.storage, index, storable)
}

func (a *BasicArray) Remove(index uint64) (Value, error) {
	storable, err := a.root.Remove(a.storage, index)
	if err != nil {
		return nil, err
	}
	return storable.StoredValue(a.storage)
}

func (a *BasicArray) Count() uint64 {
	return a.root.Count()
}

func (a *BasicArray) String() string {
	return a.root.String()
}
