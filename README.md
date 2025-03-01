
<p align="center">
  <img src="https://raw.githubusercontent.com/onflow/atree/ec159f3a81cbc6f1338f7f594987f483ddd1e0bd/files/logo.png" width="150"/>
</p>

<p align="center">
  <a href=https://github.com/onflow/atree/actions?query=workflow%3Aci><img src=https://github.com/onflow/atree/workflows/ci/badge.svg/></a>
  <a href=https://github.com/onflow/atree/actions?query=workflow%3Alinters><img src=https://github.com/onflow/atree/workflows/linters/badge.svg></a>
  <a href=https://github.com/onflow/atree/actions/workflows/codeql-analysis.yml><img src=https://github.com/onflow/atree/actions/workflows/codeql-analysis.yml/badge.svg></a>
</p>

# Atree 

*Atree* provides scalable arrays and scalable ordered maps.  It is used by [Cadence](https://github.com/onflow/cadence) in the [Flow](https://github.com/onflow/flow-go) blockchain.

Inspired by patterns used in modern variants of B+ Trees, Atree provides two types of data structures: Ordered Map Type (OMT) and Scalable Array Type (SAT).

Scalable Array Type (SAT) is a heterogeneous variable-size array, storing any type of values into a smaller ordered list of values and provides efficient functionality to lookup, insert and remove elements anywhere in the array.

Ordered Map Type (OMT) is an ordered map of key-value pairs; keys can be any hashable type and values can be any serializable value type. It supports heterogeneous key or value types (e.g. first key storing a boolean and second key storing a string). OMT keeps values in specific sorted order and operations are deterministic so the state of the segments after a sequence of operations are always unique.

Under the hood, Atree uses some new type of high-fanout B+ tree and some heuristics to balance the trade-off between latency of operations and the number of reads and writes.

Each data structure holds the data as several relatively fixed-size segments of bytes (also known as slabs) forming a tree and as the size of data structures grows or shrinks, it adjusts the number of segments used. After each operation, Atree tries to keep segment size within an acceptable size range by merging segments when needed (lower than min threshold) and splitting large-size slabs (above max threshold) or moving some values to neighbouring segments (rebalancing). For ordered maps and arrays with small number of elements, Atree is designed to have a very minimal overhead in compare to less scalable standard array and ordermaps (using a single data segment at start). 

In order to minimize the number of bytes touched after each operation, Atree uses a deterministic greedy approach ("Optimistic Encasing Algorithm") to postpone merge, split and rebalancing the tree as much as possible. in other words, It tolerates the tree to get unbalanced with the cost of keeping some space for future insertions or growing a segment a bit larger than what it should be which would minimize the number of segments (and bytes) that are touched at each operation.

## Example 

<p align="left">
  <img src="https://raw.githubusercontent.com/onflow/atree/e47e7e8016bd781211c01c6ec423ae9df8a34b72/files/example.jpg" width="600"/>
</p>

**1** - An ordered map metadata slab keeps the very first key hash of any children to navigate the path. It uses a combination of linear scan and binary search to find the next slab.

**2** - Similarly the array metadata slab keeps the count of each child and uses that to navigate the path.

**3** - Nested structures (e.g. map holding an array under a key) are handled by storing nested map or array as separate objects and using a one-way reference from parent to the nested object.

**4** - Extremely large objects are handled by storing them as an external data slab and using a pointer to the external data slab. This way we maintain the size requirements of slabs and preserve the performance of atree. In the future work external data slabs can be broken into a sequence of smaller size slabs. 

**5** - Atree Ordered Map uses a collision handling design that is performant and resilient against hash-flooding attacks. It uses multi-level hashing that combines a fast 64-bit non-cryptographic hash with a 256-bit cryptographic hash. For speed, the cryptographic hash is only computed if there's a collision. For smaller storage size, the digests are divided into 64-bit segments with only the minimum required being stored. Collisions that cannot be resolved by hashes will eventually use linear lookup, but that is very unlikely as it would require collisions on two different hashes (CircleHash64 + BLAKE3) from the same input.

**6** - Forwarding data slab pointers are used to make sequential iterations more efficient.

## API Reference

Atree's API is [documented](https://pkg.go.dev/github.com/onflow/atree#section-documentation) with godoc at pkg.go.dev and will be updated when new versions of Atree are tagged.  

## Contributing

If you would like to contribute to Atree, have a look at the [contributing guide](https://github.com/onflow/atree/blob/main/CONTRIBUTING.md).

Additionally, all non-error code paths must be covered by tests.  And pull requests should not lower the code coverage percent.

## License

The Atree library is licensed under the terms of the Apache license. See [LICENSE](LICENSE) for more information.

Logo is based on the artwork of Raisul Hadi licensed under Creative Commons.

Copyright © 2021 Dapper Labs, Inc.

