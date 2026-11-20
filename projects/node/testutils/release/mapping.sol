// SPDX-License-Identifier: MIT
pragma solidity ^0.8.3;

contract bdagMapping{

    struct BurnDetail {
        uint256 Amount;
        uint256 Time;
        uint256 Order;
        uint256 Height;
    }
    mapping(bytes => uint256) public BdagMappingCount; // index 0

    // all lockUsers mapping Amounts
    mapping(bytes => mapping(uint256 => BurnDetail)) public bdagMappingAmounts;

    // query amount
    function queryAmount(bytes memory _Hash160) external view returns (uint256) {
        uint256 a = 0;
        for (uint256 i=0;i<BdagMappingCount[_Hash160];i++){
            a += bdagMappingAmounts[_Hash160][i].Amount;
        }
        return a;
    }

    // query burn detail
    function queryBurnDetails(bytes memory _Hash160) external view returns (BurnDetail[] memory) {
        BurnDetail[] memory bd = new BurnDetail[](BdagMappingCount[_Hash160]);
        for (uint256 i=0;i<BdagMappingCount[_Hash160];i++){
            bd[i] = bdagMappingAmounts[_Hash160][i];
        }
        return bd;
    }

}