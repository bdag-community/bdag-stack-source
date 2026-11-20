// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;
import {ECDSA} from "@openzeppelin/contracts/utils/cryptography/ECDSA.sol";
import {ERC1155Holder} from "@openzeppelin/contracts/token/ERC1155/utils/ERC1155Holder.sol";

/// @dev Test-only ERC1271/1155 receiver for exercising contract-held credentials.
contract EpochCredentialSignatureWallet is ERC1155Holder {
    address public immutable signer;
    constructor(address signer_) { signer = signer_; }
    function isValidSignature(bytes32 hash, bytes calldata signature) external view returns (bytes4) {
        (address recovered, ECDSA.RecoverError err,) = ECDSA.tryRecover(hash, signature);
        return err == ECDSA.RecoverError.NoError && recovered == signer ? bytes4(0x1626ba7e) : bytes4(0xffffffff);
    }
    function execute(address target, bytes calldata data) external payable {
        require(msg.sender == signer, "test signer only");
        (bool ok, bytes memory result) = target.call{value: msg.value}(data);
        if (!ok) assembly { revert(add(result, 32), mload(result)) }
    }
}
