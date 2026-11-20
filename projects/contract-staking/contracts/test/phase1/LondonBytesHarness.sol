// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {Bytes} from "@openzeppelin/contracts/utils/Bytes.sol";

contract LondonBytesHarness {
    function slice(bytes memory value, uint256 start, uint256 end) external pure returns (bytes memory) {
        return Bytes.slice(value, start, end);
    }
}
