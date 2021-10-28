/*
 * Atree - Scalable Arrays and Ordered Maps
 *
 * Copyright 2021 Dapper Labs, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package atree

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Seed only once and print seed for easier debugging.
func init() {
	seed := time.Now().UnixNano()
	rand.Seed(seed)
	fmt.Printf("seed: 0x%x\n", seed)
}

func verifyEmptyArray(
	t *testing.T,
	storage *PersistentSlabStorage,
	typeInfo TypeInfo,
	address Address,
	array *Array,
) {
	verifyArray(t, storage, typeInfo, address, array, nil)
}

// verifyArray verifies array elements and validates serialization and in-memory slab tree.
func verifyArray(
	t *testing.T,
	storage *PersistentSlabStorage,
	typeInfo TypeInfo,
	address Address,
	array *Array,
	values []Value,
) {
	require.True(t, typeInfoComparator(typeInfo, array.Type()))
	require.Equal(t, address, array.Address())
	require.Equal(t, uint64(len(values)), array.Count())

	var err error

	// Verify array elements
	for i, v := range values {
		s, err := array.Get(uint64(i))
		require.NoError(t, err)

		e, err := s.StoredValue(array.Storage)
		require.NoError(t, err)

		valueEqual(t, typeInfoComparator, v, e)
	}

	// Verify array elements by iterator
	i := 0
	err = array.Iterate(func(v Value) (bool, error) {
		valueEqual(t, typeInfoComparator, values[i], v)
		i++
		return true, nil
	})
	require.NoError(t, err)
	require.Equal(t, len(values), i)

	// Verify in-memory slabs
	err = ValidArray(array, typeInfo, typeInfoComparator, hashInputProvider)
	if err != nil {
		PrintArray(array)
	}
	require.NoError(t, err)

	// Verify slab serializations
	err = ValidArraySerialization(
		array,
		storage.cborDecMode,
		storage.cborEncMode,
		storage.DecodeStorable,
		storage.DecodeTypeInfo,
		func(a, b Storable) bool {
			return reflect.DeepEqual(a, b)
		},
	)
	if err != nil {
		PrintArray(array)
	}
	require.NoError(t, err)

	// Need to call Commit before calling storage.Count() for PersistentSlabStorage.
	err = storage.Commit()
	require.NoError(t, err)

	stats, err := GetArrayStats(array)
	require.NoError(t, err)
	require.Equal(t, stats.SlabCount(), uint64(storage.Count()))

	if len(values) == 0 {
		// Verify slab count for empty array
		require.Equal(t, uint64(1), stats.DataSlabCount)
		require.Equal(t, uint64(0), stats.MetaDataSlabCount)
		require.Equal(t, uint64(0), stats.StorableSlabCount)
	}
}

func validArraySerialization(array *Array, storage *PersistentSlabStorage) error {
	return ValidArraySerialization(
		array,
		storage.cborDecMode,
		storage.cborEncMode,
		storage.DecodeStorable,
		storage.DecodeTypeInfo,
		func(a, b Storable) bool {
			return reflect.DeepEqual(a, b)
		},
	)
}

func TestArrayAppendAndGet(t *testing.T) {
	// With slab size 256 bytes, number of array elements equal 4096,
	// element values equal 0-4095, array tree will be 3 levels,
	// with 14 metadata slabs, and 109 data slabs.

	SetThreshold(256)
	defer SetThreshold(1024)

	const arraySize = 4096

	typeInfo := testTypeInfo{42}
	storage := newTestPersistentStorage(t)
	address := Address{1, 2, 3, 4, 5, 6, 7, 8}

	array, err := NewArray(storage, address, typeInfo)
	require.NoError(t, err)

	values := make([]Value, arraySize)
	for i := uint64(0); i < arraySize; i++ {
		v := Uint64Value(i)
		values[i] = v
		err := array.Append(v)
		require.NoError(t, err)
	}

	verifyArray(t, storage, typeInfo, address, array, values)
}

func TestArraySetAndGet(t *testing.T) {

	t.Run("new elements with similar bytesize", func(t *testing.T) {
		const arraySize = 4096

		typeInfo := testTypeInfo{42}
		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		values := make([]Value, arraySize)
		for i := uint64(0); i < arraySize; i++ {
			v := Uint64Value(i)
			values[i] = v
			err := array.Append(v)
			require.NoError(t, err)
		}

		verifyArray(t, storage, typeInfo, address, array, values)

		for i := uint64(0); i < arraySize; i++ {
			oldValue := values[i]
			newValue := Uint64Value(i * 10)
			values[i] = newValue

			existingStorable, err := array.Set(i, newValue)
			require.NoError(t, err)

			existingValue, err := existingStorable.StoredValue(storage)
			require.NoError(t, err)
			valueEqual(t, typeInfoComparator, oldValue, existingValue)
		}

		verifyArray(t, storage, typeInfo, address, array, values)
	})

	// This tests slabs splitting and root slab reassignment caused by Set operation.
	t.Run("new elements with larger bytesize", func(t *testing.T) {
		// With slab size 256 bytes, number of array elements equal 50,
		// element values equal 0-49, array tree will be 1 level,
		// with 0 metadata slab, and 1 data slab (root).
		// When elements are overwritten with values from math.MaxUint64-49 to math.MaxUint64,
		// array tree is 2 levels, with 1 metadata slab, and 2 data slabs.

		const arraySize = 50

		SetThreshold(256)
		defer SetThreshold(1024)

		typeInfo := testTypeInfo{42}
		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		values := make([]Value, arraySize)
		for i := uint64(0); i < arraySize; i++ {
			v := Uint64Value(i)
			values[i] = v
			err := array.Append(v)
			require.NoError(t, err)
		}

		verifyArray(t, storage, typeInfo, address, array, values)

		for i := uint64(0); i < arraySize; i++ {
			oldValue := values[i]
			newValue := Uint64Value(math.MaxUint64 - arraySize + i + 1)
			values[i] = newValue

			existingStorable, err := array.Set(i, newValue)
			require.NoError(t, err)

			existingValue, err := existingStorable.StoredValue(storage)
			require.NoError(t, err)
			valueEqual(t, typeInfoComparator, oldValue, existingValue)
		}

		verifyArray(t, storage, typeInfo, address, array, values)
	})

	// This tests slabs merging and root slab reassignment caused by Set operation.
	t.Run("new elements with smaller bytesize", func(t *testing.T) {

		// With slab size 256 bytes, number of array elements equal 50,
		// element values equal math.MaxUint64-49 to math.MaxUint64,
		// array tree is 2 levels, with 1 metadata slab, and 2 data slabs.
		// When elements are overwritten with values from 0-49,
		// array tree will be 1 level, with 0 metadata slab, and 1 data slab (root).

		const arraySize = 50

		SetThreshold(256)
		defer SetThreshold(1024)

		typeInfo := testTypeInfo{42}
		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		values := make([]Value, arraySize)
		for i := uint64(0); i < arraySize; i++ {
			v := Uint64Value(math.MaxUint64 - arraySize + i + 1)
			values[i] = v
			err := array.Append(v)
			require.NoError(t, err)
		}

		verifyArray(t, storage, typeInfo, address, array, values)

		for i := uint64(0); i < arraySize; i++ {
			oldValue := values[i]
			newValue := Uint64Value(i)
			values[i] = newValue

			existingStorable, err := array.Set(i, newValue)
			require.NoError(t, err)

			existingValue, err := existingStorable.StoredValue(storage)
			require.NoError(t, err)
			valueEqual(t, typeInfoComparator, oldValue, existingValue)
		}

		verifyArray(t, storage, typeInfo, address, array, values)
	})
}

func TestArrayInsertAndGet(t *testing.T) {

	SetThreshold(256)
	defer SetThreshold(1024)

	t.Run("insert-first", func(t *testing.T) {

		const arraySize = 4096

		typeInfo := testTypeInfo{42}
		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		values := make([]Value, arraySize)
		for i := uint64(0); i < arraySize; i++ {
			v := Uint64Value(arraySize - i - 1)
			values[arraySize-i-1] = v
			err := array.Insert(0, v)
			require.NoError(t, err)
		}

		verifyArray(t, storage, typeInfo, address, array, values)
	})

	t.Run("insert-last", func(t *testing.T) {

		const arraySize = 4096

		typeInfo := testTypeInfo{42}
		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		values := make([]Value, arraySize)
		for i := uint64(0); i < arraySize; i++ {
			v := Uint64Value(i)
			values[i] = v
			err := array.Insert(i, v)
			require.NoError(t, err)
		}

		verifyArray(t, storage, typeInfo, address, array, values)
	})

	t.Run("insert", func(t *testing.T) {

		const arraySize = 4096

		typeInfo := testTypeInfo{42}
		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		values := make([]Value, 0, arraySize)
		for i := uint64(0); i < arraySize; i += 2 {
			v := Uint64Value(i)
			values = append(values, v)
			err := array.Append(v)
			require.NoError(t, err)
		}

		for i := uint64(1); i < arraySize; i += 2 {
			v := Uint64Value(i)

			values = append(values, nil)
			copy(values[i+1:], values[i:])
			values[i] = v

			err := array.Insert(i, v)
			require.NoError(t, err)
		}

		verifyArray(t, storage, typeInfo, address, array, values)
	})
}

func TestArrayRemove(t *testing.T) {
	SetThreshold(256)
	defer SetThreshold(1024)

	t.Run("remove-first", func(t *testing.T) {

		const arraySize = 4096

		typeInfo := testTypeInfo{42}
		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		values := make([]Value, arraySize)
		for i := uint64(0); i < arraySize; i++ {
			v := Uint64Value(i)
			values[i] = v
			err := array.Append(v)
			require.NoError(t, err)
		}

		require.True(t, typeInfoComparator(typeInfo, array.Type()))
		require.Equal(t, address, array.Address())
		require.Equal(t, uint64(arraySize), array.Count())

		for i := uint64(0); i < arraySize; i++ {
			existingStorable, err := array.Remove(0)
			require.NoError(t, err)

			existingValue, err := existingStorable.StoredValue(array.Storage)
			require.NoError(t, err)

			valueEqual(t, typeInfoComparator, values[i], existingValue)

			if id, ok := existingStorable.(StorageIDStorable); ok {
				err = array.Storage.Remove(StorageID(id))
				require.NoError(t, err)
			}

			require.Equal(t, arraySize-i-1, array.Count())

			if i%256 == 0 {
				verifyArray(t, storage, typeInfo, address, array, values[i+1:])
			}
		}

		verifyEmptyArray(t, storage, typeInfo, address, array)
	})

	t.Run("remove-last", func(t *testing.T) {

		const arraySize = 4096

		typeInfo := testTypeInfo{42}
		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		values := make([]Value, arraySize)
		for i := uint64(0); i < arraySize; i++ {
			v := Uint64Value(i)
			values[i] = v
			err := array.Append(v)
			require.NoError(t, err)
		}

		require.True(t, typeInfoComparator(typeInfo, array.Type()))
		require.Equal(t, address, array.Address())
		require.Equal(t, uint64(arraySize), array.Count())

		for i := arraySize - 1; i >= 0; i-- {
			existingStorable, err := array.Remove(uint64(i))
			require.NoError(t, err)

			existingValue, err := existingStorable.StoredValue(array.Storage)
			require.NoError(t, err)

			valueEqual(t, typeInfoComparator, values[i], existingValue)

			if id, ok := existingStorable.(StorageIDStorable); ok {
				err = array.Storage.Remove(StorageID(id))
				require.NoError(t, err)
			}

			require.Equal(t, uint64(i), array.Count())

			if i%256 == 0 {
				verifyArray(t, storage, typeInfo, address, array, values[:i])
			}
		}

		verifyEmptyArray(t, storage, typeInfo, address, array)
	})

	t.Run("remove", func(t *testing.T) {

		const arraySize = 4096

		typeInfo := testTypeInfo{42}
		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		values := make([]Value, arraySize)
		for i := uint64(0); i < arraySize; i++ {
			v := Uint64Value(i)
			values[i] = v
			err := array.Append(v)
			require.NoError(t, err)
		}

		require.True(t, typeInfoComparator(typeInfo, array.Type()))
		require.Equal(t, address, array.Address())
		require.Equal(t, uint64(arraySize), array.Count())

		// Remove every other elements
		for i := uint64(0); i < arraySize/2; i++ {
			v := values[i]

			existingStorable, err := array.Remove(i)
			require.NoError(t, err)

			existingValue, err := existingStorable.StoredValue(array.Storage)
			require.NoError(t, err)

			valueEqual(t, typeInfoComparator, v, existingValue)

			if id, ok := existingStorable.(StorageIDStorable); ok {
				err = array.Storage.Remove(StorageID(id))
				require.NoError(t, err)
			}

			copy(values[i:], values[i+1:])
			values = values[:len(values)-1]

			require.Equal(t, uint64(len(values)), array.Count())

			if i%256 == 0 {
				verifyArray(t, storage, typeInfo, address, array, values)
			}
		}

		require.Equal(t, arraySize/2, len(values))

		verifyArray(t, storage, typeInfo, address, array, values)
	})
}

func TestArrayIterate(t *testing.T) {

	t.Run("empty", func(t *testing.T) {
		typeInfo := testTypeInfo{42}
		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		i := uint64(0)
		err = array.Iterate(func(v Value) (bool, error) {
			i++
			return true, nil
		})
		require.NoError(t, err)
		require.Equal(t, uint64(0), i)
	})

	t.Run("append", func(t *testing.T) {
		SetThreshold(256)
		defer SetThreshold(1024)

		const arraySize = 4096

		typeInfo := testTypeInfo{42}
		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		for i := uint64(0); i < arraySize; i++ {
			err := array.Append(Uint64Value(i))
			require.NoError(t, err)
		}

		i := uint64(0)
		err = array.Iterate(func(v Value) (bool, error) {
			require.Equal(t, Uint64Value(i), v)
			i++
			return true, nil
		})
		require.NoError(t, err)
		require.Equal(t, uint64(arraySize), i)
	})

	t.Run("set", func(t *testing.T) {
		SetThreshold(256)
		defer SetThreshold(1024)

		const arraySize = 4096

		typeInfo := testTypeInfo{42}
		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		for i := uint64(0); i < arraySize; i++ {
			err := array.Append(Uint64Value(0))
			require.NoError(t, err)
		}

		for i := uint64(0); i < arraySize; i++ {
			existingStorable, err := array.Set(i, Uint64Value(i))
			require.NoError(t, err)

			existingValue, err := existingStorable.StoredValue(storage)
			require.NoError(t, err)
			require.Equal(t, Uint64Value(0), existingValue)
		}

		i := uint64(0)
		err = array.Iterate(func(v Value) (bool, error) {
			require.Equal(t, Uint64Value(i), v)
			i++
			return true, nil
		})
		require.NoError(t, err)
		require.Equal(t, uint64(arraySize), i)
	})

	t.Run("insert", func(t *testing.T) {
		SetThreshold(256)
		defer SetThreshold(1024)

		const arraySize = 4096

		typeInfo := testTypeInfo{42}
		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		for i := uint64(0); i < arraySize; i += 2 {
			err := array.Append(Uint64Value(i))
			require.NoError(t, err)
		}

		for i := uint64(1); i < arraySize; i += 2 {
			err := array.Insert(i, Uint64Value(i))
			require.NoError(t, err)
		}

		i := uint64(0)
		err = array.Iterate(func(v Value) (bool, error) {
			require.Equal(t, Uint64Value(i), v)
			i++
			return true, nil
		})
		require.NoError(t, err)
		require.Equal(t, uint64(arraySize), i)
	})

	t.Run("remove", func(t *testing.T) {
		SetThreshold(256)
		defer SetThreshold(1024)

		const arraySize = 4096

		typeInfo := testTypeInfo{42}
		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		for i := uint64(0); i < arraySize; i++ {
			err := array.Append(Uint64Value(i))
			require.NoError(t, err)
		}

		// Remove every other elements
		for i := uint64(0); i < array.Count(); i++ {
			storable, err := array.Remove(i)
			require.NoError(t, err)
			require.Equal(t, Uint64Value(i*2), storable)
		}

		i := uint64(0)
		j := uint64(1)
		err = array.Iterate(func(v Value) (bool, error) {
			require.Equal(t, Uint64Value(j), v)
			i++
			j += 2
			return true, nil
		})
		require.NoError(t, err)
		require.Equal(t, uint64(arraySize/2), i)
	})

	t.Run("stop", func(t *testing.T) {

		typeInfo := testTypeInfo{42}
		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		const count = 10
		for i := uint64(0); i < count; i++ {
			err := array.Append(Uint64Value(i))
			require.NoError(t, err)
		}

		i := 0
		err = array.Iterate(func(_ Value) (bool, error) {
			if i == count/2 {
				return false, nil
			}
			i++
			return true, nil
		})
		require.NoError(t, err)
		require.Equal(t, count/2, i)
	})

	t.Run("error", func(t *testing.T) {

		typeInfo := testTypeInfo{42}
		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		const count = 10
		for i := uint64(0); i < count; i++ {
			err := array.Append(Uint64Value(i))
			require.NoError(t, err)
		}

		testErr := errors.New("test")

		i := 0
		err = array.Iterate(func(_ Value) (bool, error) {
			if i == count/2 {
				return false, testErr
			}
			i++
			return true, nil
		})
		require.Error(t, err)
		require.Equal(t, testErr, err)
		require.Equal(t, count/2, i)
	})
}

func TestArrayRootStorageID(t *testing.T) {
	SetThreshold(256)
	defer SetThreshold(1024)

	const arraySize = 4096

	typeInfo := testTypeInfo{42}
	storage := newTestPersistentStorage(t)
	address := Address{1, 2, 3, 4, 5, 6, 7, 8}

	array, err := NewArray(storage, address, typeInfo)
	require.NoError(t, err)

	savedRootID := array.StorageID()
	require.NotEqual(t, StorageIDUndefined, savedRootID)

	// Append elements
	for i := uint64(0); i < arraySize; i++ {
		err := array.Append(Uint64Value(i))
		require.NoError(t, err)
		require.Equal(t, savedRootID, array.StorageID())
	}

	require.True(t, typeInfoComparator(typeInfo, array.Type()))
	require.Equal(t, address, array.Address())
	require.Equal(t, uint64(arraySize), array.Count())

	// Remove elements
	for i := uint64(0); i < arraySize; i++ {
		storable, err := array.Remove(0)
		require.NoError(t, err)
		require.Equal(t, Uint64Value(i), storable)
		require.Equal(t, savedRootID, array.StorageID())
	}

	require.True(t, typeInfoComparator(typeInfo, array.Type()))
	require.Equal(t, address, array.Address())
	require.Equal(t, uint64(0), array.Count())
	require.Equal(t, savedRootID, array.StorageID())
}

func TestArraySetRandomValues(t *testing.T) {

	SetThreshold(256)
	defer SetThreshold(1024)

	const arraySize = 4096

	typeInfo := testTypeInfo{42}
	storage := newTestPersistentStorage(t)
	address := Address{1, 2, 3, 4, 5, 6, 7, 8}

	array, err := NewArray(storage, address, typeInfo)
	require.NoError(t, err)

	values := make([]Value, arraySize)
	for i := uint64(0); i < arraySize; i++ {
		v := Uint64Value(i)
		values[i] = v
		err := array.Append(v)
		require.NoError(t, err)
	}

	for i := uint64(0); i < arraySize; i++ {
		oldValue := values[i]
		newValue := randomValue(int(MaxInlineArrayElementSize))
		values[i] = newValue

		existingStorable, err := array.Set(i, newValue)
		require.NoError(t, err)

		existingValue, err := existingStorable.StoredValue(storage)
		require.NoError(t, err)
		valueEqual(t, typeInfoComparator, oldValue, existingValue)
	}

	verifyArray(t, storage, typeInfo, address, array, values)
}

func TestArrayInsertRandomValues(t *testing.T) {

	SetThreshold(256)
	defer SetThreshold(1024)

	t.Run("insert-first", func(t *testing.T) {

		const arraySize = 4096

		typeInfo := testTypeInfo{42}
		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		values := make([]Value, arraySize)
		for i := uint64(0); i < arraySize; i++ {
			v := randomValue(int(MaxInlineArrayElementSize))
			values[arraySize-i-1] = v

			err := array.Insert(0, v)
			require.NoError(t, err)
		}

		verifyArray(t, storage, typeInfo, address, array, values)
	})

	t.Run("insert-last", func(t *testing.T) {

		const arraySize = 4096

		typeInfo := testTypeInfo{42}
		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		values := make([]Value, arraySize)
		for i := uint64(0); i < arraySize; i++ {
			v := randomValue(int(MaxInlineArrayElementSize))
			values[i] = v

			err := array.Insert(i, v)
			require.NoError(t, err)
		}

		verifyArray(t, storage, typeInfo, address, array, values)
	})

	t.Run("insert-random", func(t *testing.T) {

		const arraySize = 4096

		typeInfo := testTypeInfo{42}
		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		values := make([]Value, arraySize)
		for i := uint64(0); i < arraySize; i++ {
			k := rand.Intn(int(i) + 1)
			v := randomValue(int(MaxInlineArrayElementSize))

			copy(values[k+1:], values[k:])
			values[k] = v

			err := array.Insert(uint64(k), v)
			require.NoError(t, err)
		}

		verifyArray(t, storage, typeInfo, address, array, values)
	})
}

func TestArrayRemoveRandomValues(t *testing.T) {

	SetThreshold(256)
	defer SetThreshold(1024)

	const arraySize = 4096

	typeInfo := testTypeInfo{42}
	storage := newTestPersistentStorage(t)
	address := Address{1, 2, 3, 4, 5, 6, 7, 8}

	array, err := NewArray(storage, address, typeInfo)
	require.NoError(t, err)

	values := make([]Value, arraySize)
	// Insert n random values into array
	for i := uint64(0); i < arraySize; i++ {
		v := randomValue(int(MaxInlineArrayElementSize))
		values[i] = v

		err := array.Insert(i, v)
		require.NoError(t, err)
	}

	verifyArray(t, storage, typeInfo, address, array, values)

	// Remove n elements at random index
	for i := uint64(0); i < arraySize; i++ {
		k := rand.Intn(int(array.Count()))

		existingStorable, err := array.Remove(uint64(k))
		require.NoError(t, err)

		existingValue, err := existingStorable.StoredValue(storage)
		require.NoError(t, err)
		valueEqual(t, typeInfoComparator, values[k], existingValue)

		copy(values[k:], values[k+1:])
		values = values[:len(values)-1]

		if id, ok := existingStorable.(StorageIDStorable); ok {
			err = storage.Remove(StorageID(id))
			require.NoError(t, err)
		}
	}

	verifyEmptyArray(t, storage, typeInfo, address, array)
}

func testArrayAppendSetInsertRemoveRandomValues(
	t *testing.T,
	storage *PersistentSlabStorage,
	typeInfo TypeInfo,
	address Address,
	opCount int,
) (*Array, []Value) {
	const (
		ArrayAppendOp = iota
		ArrayInsertOp
		ArraySetOp
		ArrayRemoveOp
		MaxArrayOp
	)

	array, err := NewArray(storage, address, typeInfo)
	require.NoError(t, err)

	values := make([]Value, 0, opCount)
	for i := 0; i < opCount; i++ {

		var nextOp int

		for {
			nextOp = rand.Intn(MaxArrayOp)

			if array.Count() > 0 || (nextOp != ArrayRemoveOp && nextOp != ArraySetOp) {
				break
			}
		}

		switch nextOp {

		case ArrayAppendOp:
			v := randomValue(int(MaxInlineArrayElementSize))
			values = append(values, v)

			err := array.Append(v)
			require.NoError(t, err)

		case ArraySetOp:
			k := rand.Intn(int(array.Count()))
			v := randomValue(int(MaxInlineArrayElementSize))

			oldV := values[k]

			values[k] = v

			existingStorable, err := array.Set(uint64(k), v)
			require.NoError(t, err)

			existingValue, err := existingStorable.StoredValue(storage)
			require.NoError(t, err)
			valueEqual(t, typeInfoComparator, oldV, existingValue)

			if id, ok := existingStorable.(StorageIDStorable); ok {
				err = storage.Remove(StorageID(id))
				require.NoError(t, err)
			}

		case ArrayInsertOp:
			k := rand.Intn(int(array.Count() + 1))
			v := randomValue(int(MaxInlineArrayElementSize))

			if k == int(array.Count()) {
				values = append(values, v)
			} else {
				values = append(values, nil)
				copy(values[k+1:], values[k:])
				values[k] = v
			}

			err := array.Insert(uint64(k), v)
			require.NoError(t, err)

		case ArrayRemoveOp:
			k := rand.Intn(int(array.Count()))

			existingStorable, err := array.Remove(uint64(k))
			require.NoError(t, err)

			existingValue, err := existingStorable.StoredValue(storage)
			require.NoError(t, err)
			valueEqual(t, typeInfoComparator, values[k], existingValue)

			copy(values[k:], values[k+1:])
			values = values[:len(values)-1]

			if id, ok := existingStorable.(StorageIDStorable); ok {
				err = storage.Remove(StorageID(id))
				require.NoError(t, err)
			}
		}

		require.Equal(t, uint64(len(values)), array.Count())
		require.True(t, typeInfoComparator(typeInfo, array.Type()))
		require.Equal(t, address, array.Address())
	}

	return array, values
}

func TestArrayAppendSetInsertRemoveRandomValues(t *testing.T) {

	SetThreshold(256)
	defer SetThreshold(1024)

	const opCount = 4096

	typeInfo := testTypeInfo{42}
	storage := newTestPersistentStorage(t)
	address := Address{1, 2, 3, 4, 5, 6, 7, 8}

	array, values := testArrayAppendSetInsertRemoveRandomValues(t, storage, typeInfo, address, opCount)
	verifyArray(t, storage, typeInfo, address, array, values)
}

func TestArrayNestedArrayMap(t *testing.T) {

	SetThreshold(256)
	defer SetThreshold(1024)

	t.Run("small array", func(t *testing.T) {

		const arraySize = 4096

		nestedTypeInfo := testTypeInfo{43}
		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		// Create a list of arrays with 2 elements.
		nestedArrays := make([]Value, arraySize)
		for i := uint64(0); i < arraySize; i++ {
			nested, err := NewArray(storage, address, nestedTypeInfo)
			require.NoError(t, err)

			err = nested.Append(Uint64Value(i))
			require.NoError(t, err)

			require.True(t, nested.root.IsData())

			nestedArrays[i] = nested
		}

		typeInfo := testTypeInfo{42}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		for _, a := range nestedArrays {
			err := array.Append(a)
			require.NoError(t, err)
		}

		verifyArray(t, storage, typeInfo, address, array, nestedArrays)
	})

	t.Run("big array", func(t *testing.T) {

		const arraySize = 4096

		nestedTypeInfo := testTypeInfo{43}
		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		nestedArrays := make([]*Array, arraySize)
		for i := uint64(0); i < arraySize; i++ {
			nested, err := NewArray(storage, address, nestedTypeInfo)
			require.NoError(t, err)

			for i := uint64(0); i < 40; i++ {
				err := nested.Append(Uint64Value(math.MaxUint64))
				require.NoError(t, err)
			}

			require.False(t, nested.root.IsData())

			nestedArrays[i] = nested
		}

		typeInfo := testTypeInfo{42}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)
		for _, a := range nestedArrays {
			err := array.Append(a)
			require.NoError(t, err)
		}

		for i := uint64(0); i < arraySize; i++ {
			existingStorable, err := array.Get(i)
			require.NoError(t, err)

			existingValue, err := existingStorable.StoredValue(storage)
			require.NoError(t, err)
			arrayEqual(t, typeInfoComparator, nestedArrays[i], existingValue)
		}

		require.True(t, typeInfoComparator(typeInfo, array.Type()))
		require.Equal(t, address, array.Address())
		require.Equal(t, uint64(arraySize), array.Count())

		err = ValidArray(array, typeInfo, typeInfoComparator, hashInputProvider)
		if err != nil {
			PrintArray(array)
		}
		require.NoError(t, err)

		err = validArraySerialization(array, storage)
		if err != nil {
			PrintArray(array)
		}
		require.NoError(t, err)
	})

	t.Run("small map", func(t *testing.T) {

		const arraySize = 4096

		nestedTypeInfo := testTypeInfo{43}

		storage := newTestPersistentStorage(t)

		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		nestedMaps := make([]Value, arraySize)
		for i := uint64(0); i < arraySize; i++ {
			nested, err := NewMap(storage, address, NewDefaultDigesterBuilder(), nestedTypeInfo)
			require.NoError(t, err)

			storable, err := nested.Set(compare, hashInputProvider, Uint64Value(i), Uint64Value(i*2))
			require.NoError(t, err)
			require.Nil(t, storable)

			require.True(t, nested.root.IsData())

			nestedMaps[i] = nested
		}

		typeInfo := testTypeInfo{42}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		for _, a := range nestedMaps {
			err := array.Append(a)
			require.NoError(t, err)
		}

		verifyArray(t, storage, typeInfo, address, array, nestedMaps)
	})

	t.Run("big map", func(t *testing.T) {

		const arraySize = 4096

		nestedTypeInfo := testTypeInfo{43}
		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		nestedMaps := make([]*OrderedMap, arraySize)
		for i := uint64(0); i < arraySize; i++ {
			nested, err := NewMap(storage, address, NewDefaultDigesterBuilder(), nestedTypeInfo)
			require.NoError(t, err)

			for i := uint64(0); i < 25; i++ {
				storable, err := nested.Set(compare, hashInputProvider, Uint64Value(i), Uint64Value(i*2))
				require.NoError(t, err)
				require.Nil(t, storable)
			}

			require.False(t, nested.root.IsData())

			nestedMaps[i] = nested
		}

		typeInfo := testTypeInfo{42}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)
		for _, a := range nestedMaps {
			err := array.Append(a)
			require.NoError(t, err)
		}

		for i := uint64(0); i < arraySize; i++ {
			existingStorable, err := array.Get(i)
			require.NoError(t, err)

			existingValue, err := existingStorable.StoredValue(storage)
			require.NoError(t, err)
			valueEqual(t, typeInfoComparator, nestedMaps[i], existingValue)
		}

		require.True(t, typeInfoComparator(typeInfo, array.Type()))
		require.Equal(t, address, array.Address())
		require.Equal(t, uint64(arraySize), array.Count())

		err = ValidArray(array, typeInfo, typeInfoComparator, hashInputProvider)
		if err != nil {
			PrintArray(array)
		}
		require.NoError(t, err)

		err = validArraySerialization(array, storage)
		if err != nil {
			PrintArray(array)
		}
		require.NoError(t, err)
	})
}

func TestArrayEncodeDecode(t *testing.T) {

	SetThreshold(256)
	defer SetThreshold(1024)

	t.Run("empty", func(t *testing.T) {
		typeInfo := testTypeInfo{42}
		storage := newTestBasicStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		expectedData := []byte{
			// extra data
			// version
			0x00,
			// extra data flag
			0x80,
			// array of extra data
			0x81,
			// type info
			0x18, 0x2a,

			// version
			0x00,
			// array data slab flag
			0x80,
			// CBOR encoded array head (fixed size 3 byte)
			0x99, 0x00, 0x00,
		}

		slabData, err := storage.Encode()
		require.NoError(t, err)
		require.Equal(t, 1, len(slabData))
		require.Equal(t, expectedData, slabData[array.StorageID()])

		// Decode data to new storage
		storage2 := newTestPersistentStorageWithData(t, slabData)

		// Test new array from storage2
		array2, err := NewArrayWithRootID(storage2, array.StorageID())
		require.NoError(t, err)

		verifyEmptyArray(t, storage2, typeInfo, address, array2)
	})

	t.Run("dataslab as root", func(t *testing.T) {
		typeInfo := testTypeInfo{42}
		storage := newTestBasicStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		v := Uint64Value(0)
		values := []Value{v}
		err = array.Append(v)
		require.NoError(t, err)

		expectedData := []byte{
			// extra data
			// version
			0x00,
			// extra data flag
			0x80,
			// array of extra data
			0x81,
			// type info
			0x18, 0x2a,

			// version
			0x00,
			// array data slab flag
			0x80,
			// CBOR encoded array head (fixed size 3 byte)
			0x99, 0x00, 0x01,
			// CBOR encoded array elements
			0xd8, 0xa4, 0x00,
		}

		slabData, err := storage.Encode()
		require.NoError(t, err)
		require.Equal(t, 1, len(slabData))
		require.Equal(t, expectedData, slabData[array.StorageID()])

		// Decode data to new storage
		storage2 := newTestPersistentStorageWithData(t, slabData)

		// Test new array from storage2
		array2, err := NewArrayWithRootID(storage2, array.StorageID())
		require.NoError(t, err)

		verifyArray(t, storage2, typeInfo, address, array2, values)
	})

	t.Run("has pointers", func(t *testing.T) {
		typeInfo := testTypeInfo{42}
		storage := newTestBasicStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		const arraySize = 20
		values := make([]Value, arraySize)
		for i := uint64(0); i < arraySize-1; i++ {
			v := NewStringValue(strings.Repeat("a", 22))
			values[i] = v
			err := array.Append(v)
			require.NoError(t, err)
		}

		typeInfo2 := testTypeInfo{43}

		nestedArray, err := NewArray(storage, address, typeInfo2)
		require.NoError(t, err)

		err = nestedArray.Append(Uint64Value(0))
		require.NoError(t, err)

		values[arraySize-1] = nestedArray

		err = array.Append(nestedArray)
		require.NoError(t, err)

		require.Equal(t, uint64(arraySize), array.Count())
		require.Equal(t, uint64(1), nestedArray.Count())

		id1 := StorageID{Address: address, Index: StorageIndex{0, 0, 0, 0, 0, 0, 0, 1}}
		id2 := StorageID{Address: address, Index: StorageIndex{0, 0, 0, 0, 0, 0, 0, 2}}
		id3 := StorageID{Address: address, Index: StorageIndex{0, 0, 0, 0, 0, 0, 0, 3}}
		id4 := StorageID{Address: address, Index: StorageIndex{0, 0, 0, 0, 0, 0, 0, 4}}

		// Expected serialized slab data with storage id
		expected := map[StorageID][]byte{

			// (metadata slab) headers: [{id:2 size:228 count:9} {id:3 size:270 count:11} ]
			id1: {
				// extra data
				// version
				0x00,
				// extra data flag
				0x81,
				// array of extra data
				0x81,
				// type info
				0x18, 0x2a,

				// version
				0x00,
				// array meta data slab flag
				0x81,
				// child header count
				0x00, 0x02,
				// child header 1 (storage id, count, size)
				0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02,
				0x00, 0x00, 0x00, 0x09,
				0x00, 0x00, 0x00, 0xe4,
				// child header 2
				0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x03,
				0x00, 0x00, 0x00, 0x0b,
				0x00, 0x00, 0x01, 0x0e,
			},

			// (data slab) next: 3, data: [aaaaaaaaaaaaaaaaaaaaaa ... aaaaaaaaaaaaaaaaaaaaaa]
			id2: {
				// version
				0x00,
				// array data slab flag
				0x00,
				// next storage id
				0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x03,
				// CBOR encoded array head (fixed size 3 byte)
				0x99, 0x00, 0x09,
				// CBOR encoded array elements
				0x76, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61,
				0x76, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61,
				0x76, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61,
				0x76, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61,
				0x76, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61,
				0x76, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61,
				0x76, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61,
				0x76, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61,
				0x76, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61,
			},

			// (data slab) next: 0, data: [aaaaaaaaaaaaaaaaaaaaaa ... StorageID(...)]
			id3: {
				// version
				0x00,
				// array data slab flag
				0x40,
				// next storage id
				0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
				// CBOR encoded array head (fixed size 3 byte)
				0x99, 0x00, 0x0b,
				// CBOR encoded array elements
				0x76, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61,
				0x76, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61,
				0x76, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61,
				0x76, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61,
				0x76, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61,
				0x76, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61,
				0x76, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61,
				0x76, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61,
				0x76, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61,
				0x76, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61,
				0xd8, 0xff, 0x50, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x04,
			},

			// (data slab) next: 0, data: [0]
			id4: {
				// extra data
				// version
				0x00,
				// extra data flag
				0x80,
				// array of extra data
				0x81,
				// type info
				0x18, 0x2b,

				// version
				0x00,
				// array data slab flag
				0x80,
				// CBOR encoded array head (fixed size 3 byte)
				0x99, 0x00, 0x01,
				// CBOR encoded array elements
				0xd8, 0xa4, 0x00,
			},
		}

		m, err := storage.Encode()
		require.NoError(t, err)
		require.Equal(t, len(expected), len(m))
		require.Equal(t, expected[id1], m[id1])
		require.Equal(t, expected[id2], m[id2])
		require.Equal(t, expected[id3], m[id3])
		require.Equal(t, expected[id4], m[id4])

		// Decode data to new storage
		storage2 := newTestPersistentStorageWithData(t, m)

		// Test new array from storage2
		array2, err := NewArrayWithRootID(storage2, array.StorageID())
		require.NoError(t, err)

		verifyArray(t, storage2, typeInfo, address, array2, values)
	})
}

func TestArrayEncodeDecodeRandomValues(t *testing.T) {

	SetThreshold(256)
	defer SetThreshold(1024)

	const opCount = 8192

	typeInfo := testTypeInfo{42}
	storage := newTestPersistentStorage(t)
	address := Address{1, 2, 3, 4, 5, 6, 7, 8}

	array, values := testArrayAppendSetInsertRemoveRandomValues(t, storage, typeInfo, address, opCount)

	verifyArray(t, storage, typeInfo, address, array, values)

	// Decode data to new storage
	storage2 := newTestPersistentStorageWithBaseStorage(t, storage.baseStorage)

	// Test new array from storage2
	array2, err := NewArrayWithRootID(storage2, array.StorageID())
	require.NoError(t, err)

	verifyArray(t, storage2, typeInfo, address, array2, values)
}

func TestEmptyArray(t *testing.T) {

	t.Parallel()

	typeInfo := testTypeInfo{42}
	address := Address{1, 2, 3, 4, 5, 6, 7, 8}
	storage := newTestBasicStorage(t)

	array, err := NewArray(storage, address, typeInfo)
	require.NoError(t, err)

	t.Run("get", func(t *testing.T) {
		s, err := array.Get(0)
		require.Error(t, err, IndexOutOfBoundsError{})
		require.Nil(t, s)
	})

	t.Run("set", func(t *testing.T) {
		s, err := array.Set(0, Uint64Value(0))
		require.Error(t, err, IndexOutOfBoundsError{})
		require.Nil(t, s)
	})

	t.Run("insert", func(t *testing.T) {
		err := array.Insert(1, Uint64Value(0))
		require.Error(t, err, IndexOutOfBoundsError{})
	})

	t.Run("remove", func(t *testing.T) {
		s, err := array.Remove(0)
		require.Error(t, err, IndexOutOfBoundsError{})
		require.Nil(t, s)
	})

	t.Run("iterate", func(t *testing.T) {
		i := uint64(0)
		err := array.Iterate(func(v Value) (bool, error) {
			i++
			return true, nil
		})
		require.NoError(t, err)
		require.Equal(t, uint64(0), i)
	})

	t.Run("count", func(t *testing.T) {
		count := array.Count()
		require.Equal(t, uint64(0), count)
	})

	t.Run("type", func(t *testing.T) {
		require.True(t, typeInfoComparator(typeInfo, array.Type()))
	})

	// TestArrayEncodeDecode/empty tests empty array encoding and decoding
}

func TestArrayStringElement(t *testing.T) {

	t.Parallel()

	t.Run("inline", func(t *testing.T) {

		const arraySize = 4096

		stringSize := int(MaxInlineArrayElementSize - 3)

		values := make([]Value, arraySize)
		for i := uint64(0); i < arraySize; i++ {
			s := randStr(stringSize)
			values[i] = NewStringValue(s)
		}

		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}
		typeInfo := testTypeInfo{42}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		for i := uint64(0); i < arraySize; i++ {
			err := array.Append(values[i])
			require.NoError(t, err)
		}

		verifyArray(t, storage, typeInfo, address, array, values)

		stats, err := GetArrayStats(array)
		require.NoError(t, err)
		require.Equal(t, uint64(0), stats.StorableSlabCount)
	})

	t.Run("external slab", func(t *testing.T) {

		const arraySize = 4096

		stringSize := int(MaxInlineArrayElementSize + 512)

		values := make([]Value, arraySize)
		for i := uint64(0); i < arraySize; i++ {
			s := randStr(stringSize)
			values[i] = NewStringValue(s)
		}

		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}
		typeInfo := testTypeInfo{42}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		for i := uint64(0); i < arraySize; i++ {
			err := array.Append(values[i])
			require.NoError(t, err)
		}

		verifyArray(t, storage, typeInfo, address, array, values)

		stats, err := GetArrayStats(array)
		require.NoError(t, err)
		require.Equal(t, uint64(arraySize), stats.StorableSlabCount)
	})
}

func TestArrayStoredValue(t *testing.T) {

	const arraySize = 4096

	typeInfo := testTypeInfo{42}
	address := Address{1, 2, 3, 4, 5, 6, 7, 8}
	storage := newTestBasicStorage(t)

	array, err := NewArray(storage, address, typeInfo)
	require.NoError(t, err)

	for i := uint64(0); i < arraySize; i++ {
		err := array.Append(Uint64Value(i))
		require.NoError(t, err)
	}

	rootID := array.StorageID()

	for id := range storage.Slabs {
		slab, ok, err := storage.Retrieve(id)
		require.NoError(t, err)
		require.True(t, ok)

		value, err := slab.StoredValue(storage)

		if id == rootID {
			require.NoError(t, err)

			array2, ok := value.(*Array)
			require.True(t, ok)

			require.True(t, typeInfoComparator(typeInfo, array2.Type()))
			require.Equal(t, address, array2.Address())
			require.Equal(t, uint64(arraySize), array2.Count())

			for i := uint64(0); i < arraySize; i++ {
				v, err := array2.Get(i)
				require.NoError(t, err)
				require.Equal(t, Uint64Value(i), v)
			}
		} else {
			require.Error(t, err)
			require.Nil(t, value)
		}
	}
}

func TestArrayPopIterate(t *testing.T) {

	t.Run("empty", func(t *testing.T) {
		typeInfo := testTypeInfo{42}
		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		i := uint64(0)
		err = array.PopIterate(func(v Storable) {
			i++
		})
		require.NoError(t, err)
		require.Equal(t, uint64(0), i)

		verifyEmptyArray(t, storage, typeInfo, address, array)
	})

	t.Run("root-dataslab", func(t *testing.T) {

		const arraySize = 10

		typeInfo := testTypeInfo{42}
		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		values := make([]Value, arraySize)
		for i := uint64(0); i < arraySize; i++ {
			v := Uint64Value(i)
			values[i] = v
			err := array.Append(v)
			require.NoError(t, err)
		}

		i := 0
		err = array.PopIterate(func(v Storable) {
			vv, err := v.StoredValue(storage)
			require.NoError(t, err)
			valueEqual(t, typeInfoComparator, values[arraySize-i-1], vv)
			i++
		})
		require.NoError(t, err)
		require.Equal(t, arraySize, i)

		verifyEmptyArray(t, storage, typeInfo, address, array)
	})

	t.Run("root-metaslab", func(t *testing.T) {
		SetThreshold(256)
		defer SetThreshold(1024)

		const arraySize = 4096

		typeInfo := testTypeInfo{42}
		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		values := make([]Value, arraySize)
		for i := uint64(0); i < arraySize; i++ {
			v := Uint64Value(i)
			values[i] = v
			err := array.Append(v)
			require.NoError(t, err)
		}

		i := 0
		err = array.PopIterate(func(v Storable) {
			vv, err := v.StoredValue(storage)
			require.NoError(t, err)
			valueEqual(t, typeInfoComparator, values[arraySize-i-1], vv)
			i++
		})
		require.NoError(t, err)
		require.Equal(t, arraySize, i)

		verifyEmptyArray(t, storage, typeInfo, address, array)
	})
}

func TestArrayFromBatchData(t *testing.T) {

	t.Run("empty", func(t *testing.T) {
		typeInfo := testTypeInfo{42}

		array, err := NewArray(
			newTestPersistentStorage(t),
			Address{1, 2, 3, 4, 5, 6, 7, 8},
			typeInfo)
		require.NoError(t, err)
		require.Equal(t, uint64(0), array.Count())

		iter, err := array.Iterator()
		require.NoError(t, err)

		// Create a new array with new storage, new address, and original array's elements.
		address := Address{2, 3, 4, 5, 6, 7, 8, 9}
		storage := newTestPersistentStorage(t)
		copied, err := NewArrayFromBatchData(
			storage,
			address,
			array.Type(),
			func() (Value, error) {
				return iter.Next()
			})
		require.NoError(t, err)
		require.NotEqual(t, copied.StorageID(), array.StorageID())

		verifyEmptyArray(t, storage, typeInfo, address, copied)
	})

	t.Run("root-dataslab", func(t *testing.T) {

		const arraySize = 10

		typeInfo := testTypeInfo{42}
		array, err := NewArray(
			newTestPersistentStorage(t),
			Address{1, 2, 3, 4, 5, 6, 7, 8},
			typeInfo)
		require.NoError(t, err)

		values := make([]Value, arraySize)
		for i := uint64(0); i < arraySize; i++ {
			v := Uint64Value(i)
			values[i] = v
			err := array.Append(v)
			require.NoError(t, err)
		}

		require.Equal(t, uint64(arraySize), array.Count())

		iter, err := array.Iterator()
		require.NoError(t, err)

		// Create a new array with new storage, new address, and original array's elements.
		address := Address{2, 3, 4, 5, 6, 7, 8, 9}
		storage := newTestPersistentStorage(t)
		copied, err := NewArrayFromBatchData(
			storage,
			address,
			array.Type(),
			func() (Value, error) {
				return iter.Next()
			})

		require.NoError(t, err)
		require.NotEqual(t, copied.StorageID(), array.StorageID())

		verifyArray(t, storage, typeInfo, address, copied, values)
	})

	t.Run("root-metaslab", func(t *testing.T) {
		SetThreshold(256)
		defer SetThreshold(1024)

		const arraySize = 4096

		typeInfo := testTypeInfo{42}

		array, err := NewArray(
			newTestPersistentStorage(t),
			Address{1, 2, 3, 4, 5, 6, 7, 8},
			typeInfo)
		require.NoError(t, err)

		values := make([]Value, arraySize)
		for i := uint64(0); i < arraySize; i++ {
			v := Uint64Value(i)
			values[i] = v
			err := array.Append(v)
			require.NoError(t, err)
		}

		require.Equal(t, uint64(arraySize), array.Count())

		iter, err := array.Iterator()
		require.NoError(t, err)

		address := Address{2, 3, 4, 5, 6, 7, 8, 9}
		storage := newTestPersistentStorage(t)
		copied, err := NewArrayFromBatchData(
			storage,
			address,
			array.Type(),
			func() (Value, error) {
				return iter.Next()
			})

		require.NoError(t, err)
		require.NotEqual(t, array.StorageID(), copied.StorageID())

		verifyArray(t, storage, typeInfo, address, copied, values)
	})

	t.Run("rebalance two data slabs", func(t *testing.T) {
		SetThreshold(256)
		defer SetThreshold(1024)

		typeInfo := testTypeInfo{42}

		array, err := NewArray(
			newTestPersistentStorage(t),
			Address{1, 2, 3, 4, 5, 6, 7, 8},
			typeInfo)
		require.NoError(t, err)

		var values []Value
		var v Value

		v = NewStringValue(strings.Repeat("a", int(MaxInlineArrayElementSize-2)))
		values = append(values, v)

		err = array.Insert(0, v)
		require.NoError(t, err)

		for i := 0; i < 35; i++ {
			v = Uint64Value(i)
			values = append(values, v)

			err = array.Append(v)
			require.NoError(t, err)
		}

		require.Equal(t, uint64(36), array.Count())

		iter, err := array.Iterator()
		require.NoError(t, err)

		storage := newTestPersistentStorage(t)
		address := Address{2, 3, 4, 5, 6, 7, 8, 9}
		copied, err := NewArrayFromBatchData(
			storage,
			address,
			array.Type(),
			func() (Value, error) {
				return iter.Next()
			})

		require.NoError(t, err)
		require.NotEqual(t, array.StorageID(), copied.StorageID())

		verifyArray(t, storage, typeInfo, address, copied, values)
	})

	t.Run("merge two data slabs", func(t *testing.T) {
		SetThreshold(256)
		defer SetThreshold(1024)

		typeInfo := testTypeInfo{42}

		array, err := NewArray(
			newTestPersistentStorage(t),
			Address{1, 2, 3, 4, 5, 6, 7, 8},
			typeInfo)
		require.NoError(t, err)

		var values []Value
		var v Value
		for i := 0; i < 35; i++ {
			v = Uint64Value(i)
			values = append(values, v)
			err = array.Append(v)
			require.NoError(t, err)
		}

		v = NewStringValue(strings.Repeat("a", int(MaxInlineArrayElementSize-2)))
		values = append(values, nil)
		copy(values[25+1:], values[25:])
		values[25] = v

		err = array.Insert(25, v)
		require.NoError(t, err)

		require.Equal(t, uint64(36), array.Count())

		iter, err := array.Iterator()
		require.NoError(t, err)

		storage := newTestPersistentStorage(t)
		address := Address{2, 3, 4, 5, 6, 7, 8, 9}
		copied, err := NewArrayFromBatchData(
			storage,
			address,
			array.Type(),
			func() (Value, error) {
				return iter.Next()
			})

		require.NoError(t, err)
		require.NotEqual(t, array.StorageID(), copied.StorageID())

		verifyArray(t, storage, typeInfo, address, copied, values)
	})

	t.Run("random", func(t *testing.T) {
		SetThreshold(256)
		defer SetThreshold(1024)

		const arraySize = 4096

		typeInfo := testTypeInfo{42}

		array, err := NewArray(
			newTestPersistentStorage(t),
			Address{1, 2, 3, 4, 5, 6, 7, 8},
			typeInfo)
		require.NoError(t, err)

		values := make([]Value, arraySize)
		for i := uint64(0); i < arraySize; i++ {
			v := randomValue(int(MaxInlineArrayElementSize))
			values[i] = v

			err := array.Append(v)
			require.NoError(t, err)
		}

		require.Equal(t, uint64(arraySize), array.Count())

		iter, err := array.Iterator()
		require.NoError(t, err)

		storage := newTestPersistentStorage(t)

		address := Address{2, 3, 4, 5, 6, 7, 8, 9}
		copied, err := NewArrayFromBatchData(
			storage,
			address,
			array.Type(),
			func() (Value, error) {
				return iter.Next()
			})

		require.NoError(t, err)
		require.NotEqual(t, array.StorageID(), copied.StorageID())

		verifyArray(t, storage, typeInfo, address, copied, values)
	})

	t.Run("data slab too large", func(t *testing.T) {
		// Slab size must not exceed maxThreshold.
		// We cannot make this problem happen after Atree Issue #193
		// was fixed by PR #194 & PR #197. This test is to catch regressions.

		SetThreshold(256)
		defer SetThreshold(1024)

		typeInfo := testTypeInfo{42}
		array, err := NewArray(
			newTestPersistentStorage(t),
			Address{1, 2, 3, 4, 5, 6, 7, 8},
			typeInfo)
		require.NoError(t, err)

		var values []Value
		var v Value

		v = NewStringValue(randStr(int(MaxInlineArrayElementSize - 2)))
		values = append(values, v)
		err = array.Append(v)
		require.NoError(t, err)

		v = NewStringValue(randStr(int(MaxInlineArrayElementSize - 2)))
		values = append(values, v)
		err = array.Append(v)
		require.NoError(t, err)

		v = NewStringValue(randStr(int(MaxInlineArrayElementSize - 2)))
		values = append(values, v)
		err = array.Append(v)
		require.NoError(t, err)

		iter, err := array.Iterator()
		require.NoError(t, err)

		storage := newTestPersistentStorage(t)
		address := Address{2, 3, 4, 5, 6, 7, 8, 9}
		copied, err := NewArrayFromBatchData(
			storage,
			address,
			array.Type(),
			func() (Value, error) {
				return iter.Next()
			})

		require.NoError(t, err)
		require.NotEqual(t, array.StorageID(), copied.StorageID())

		verifyArray(t, storage, typeInfo, address, copied, values)
	})
}

func TestArrayNestedStorables(t *testing.T) {

	t.Parallel()

	typeInfo := testTypeInfo{42}

	const arraySize = 1024 * 4

	storage := newTestBasicStorage(t)
	address := Address{1, 2, 3, 4, 5, 6, 7, 8}

	array, err := NewArray(storage, address, typeInfo)
	require.NoError(t, err)

	for i := uint64(0); i < arraySize; i++ {

		s := strings.Repeat("a", int(i))
		v := SomeValue{Value: NewStringValue(s)}

		err := array.Append(v)
		require.NoError(t, err)
	}

	for i := uint64(0); i < arraySize; i++ {
		e, err := array.Get(i)
		require.NoError(t, err)

		v, err := e.StoredValue(storage)
		require.NoError(t, err)

		someValue, ok := v.(SomeValue)
		require.True(t, ok)

		s, ok := someValue.Value.(StringValue)
		require.True(t, ok)

		require.Equal(t, strings.Repeat("a", int(i)), s.str)
	}

	require.True(t, typeInfoComparator(typeInfo, array.Type()))

	err = ValidArray(array, typeInfo, typeInfoComparator, hashInputProvider)
	if err != nil {
		PrintArray(array)
	}
	require.NoError(t, err)

	err = ValidArraySerialization(
		array,
		storage.cborDecMode,
		storage.cborEncMode,
		storage.DecodeStorable,
		storage.DecodeTypeInfo,
		func(a, b Storable) bool {
			return reflect.DeepEqual(a, b)
		},
	)
	if err != nil {
		PrintArray(array)
	}
	require.NoError(t, err)

	_, err = CheckStorageHealth(storage, 1)
	require.NoError(t, err)
}

func TestArrayMaxInlineElement(t *testing.T) {
	t.Parallel()

	typeInfo := testTypeInfo{42}
	storage := newTestPersistentStorage(t)
	address := Address{1, 2, 3, 4, 5, 6, 7, 8}

	array, err := NewArray(storage, address, typeInfo)
	require.NoError(t, err)

	var values []Value
	for i := 0; i < 2; i++ {
		// String length is MaxInlineArrayElementSize - 3 to account for string encoding overhead.
		v := NewStringValue(randStr(int(MaxInlineArrayElementSize - 3)))
		values = append(values, v)

		err = array.Append(v)
		require.NoError(t, err)
	}

	for i := 0; i < len(values); i++ {
		existingStorable, err := array.Get(uint64(i))
		require.NoError(t, err)

		existingValue, err := existingStorable.StoredValue(array.Storage)
		require.NoError(t, err)
		require.Equal(t, values[i], existingValue)
	}

	require.True(t, array.root.IsData())

	// Size of root data slab with two elements of max inlined size is target slab size minus
	// storage id size (next storage id is omitted in root slab), and minus 1 byte
	// (for rounding when computing max inline array element size).
	require.Equal(t, targetThreshold-storageIDSize-1, uint64(array.root.Header().size))

	err = ValidArray(array, typeInfo, typeInfoComparator, hashInputProvider)
	if err != nil {
		PrintArray(array)
	}
	require.NoError(t, err)

	err = validArraySerialization(array, storage)
	if err != nil {
		PrintArray(array)
	}
	require.NoError(t, err)
}

func TestArrayString(t *testing.T) {

	SetThreshold(256)
	defer SetThreshold(1024)

	t.Run("small", func(t *testing.T) {
		const arraySize = 6

		typeInfo := testTypeInfo{42}
		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		for i := uint64(0); i < arraySize; i++ {
			err := array.Append(Uint64Value(i))
			require.NoError(t, err)
		}

		want := `[0 1 2 3 4 5]`
		require.Equal(t, want, array.String())
	})

	t.Run("large", func(t *testing.T) {
		const arraySize = 190

		typeInfo := testTypeInfo{42}
		storage := newTestPersistentStorage(t)
		address := Address{1, 2, 3, 4, 5, 6, 7, 8}

		array, err := NewArray(storage, address, typeInfo)
		require.NoError(t, err)

		for i := uint64(0); i < arraySize; i++ {
			err := array.Append(Uint64Value(i))
			require.NoError(t, err)
		}

		wantArrayString := `[0 1 2 ... 51 52 53] [54 55 56 ... 97 98 99] [100 101 102 ... 187 188 189]`
		require.Equal(t, wantArrayString, array.String())

		wantMetaDataSlabString := `[{id:0x102030405060708.2 size:213 count:54} {id:0x102030405060708.3 size:205 count:46} {id:0x102030405060708.4 size:381 count:90}]`
		require.Equal(t, wantMetaDataSlabString, array.root.String())
	})
}
