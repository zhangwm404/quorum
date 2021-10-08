package chain

import (
	"encoding/hex"
	"errors"

	localcrypto "github.com/huo-ju/quorum/internal/pkg/crypto"
	"github.com/huo-ju/quorum/internal/pkg/nodectx"
	quorumpb "github.com/huo-ju/quorum/internal/pkg/pb"
	logging "github.com/ipfs/go-log/v2"
	"google.golang.org/protobuf/proto"
)

type MolassesUser struct {
	//grp      *Group
	grpItem  *quorumpb.GroupItem
	nodename string
	cIface   ChainMolassesIface
}

var molauser_log = logging.Logger("user")

func (user *MolassesUser) Init(item *quorumpb.GroupItem, nodename string, iface ChainMolassesIface) {
	user.grpItem = item
	user.nodename = nodename
	user.cIface = iface
}

func (user *MolassesUser) UpdAnnounce(item *quorumpb.AnnounceItem) (string, error) {
	molauser_log.Infof("UpdAnnounce called")
	return user.cIface.GetProducerTrxMgr().SendAnnounceTrx(item)
}

func (user *MolassesUser) UpdBlkList(item *quorumpb.DenyUserItem) (string, error) {
	molauser_log.Infof("UpdBlkList called")
	return user.cIface.GetProducerTrxMgr().SendUpdAuthTrx(item)
}

func (user *MolassesUser) UpdSchema(item *quorumpb.SchemaItem) (string, error) {
	molauser_log.Infof("UpdSchema called")
	return user.cIface.GetProducerTrxMgr().SendUpdSchemaTrx(item)
}

func (user *MolassesUser) UpdProducer(item *quorumpb.ProducerItem) (string, error) {
	molauser_log.Infof("UpdSchema called")
	return user.cIface.GetProducerTrxMgr().SendRegProducerTrx(item)
}

func (user *MolassesUser) PostToGroup(content proto.Message) (string, error) {
	molauser_log.Infof("PostToGroup called")
	return user.cIface.GetProducerTrxMgr().PostAny(content)
}

func (user *MolassesUser) AddBlock(block *quorumpb.Block) error {
	molauser_log.Infof("AddBlock called")

	//check if block is already in chain
	isSaved, err := nodectx.GetDbMgr().IsBlockExist(block.BlockId, false, user.nodename)
	if err != nil {
		return err
	}

	if isSaved {
		return errors.New("Block already saved, ignore")
	}

	//check if block is in cache
	isCached, err := nodectx.GetDbMgr().IsBlockExist(block.BlockId, true, user.nodename)
	if err != nil {
		return err
	}

	if isCached {
		return errors.New("Block already cached, ignore")
	}

	//Save block to cache
	err = nodectx.GetDbMgr().AddBlock(block, true, user.nodename)
	if err != nil {
		return err
	}

	//check if parent of block exist
	parentExist, err := nodectx.GetDbMgr().IsParentExist(block.PrevBlockId, false, user.nodename)
	if err != nil {
		return err
	}

	if !parentExist {
		molauser_log.Infof("Block Parent not exist, sync backward")
		return errors.New("PARENT_NOT_EXIST")
	}

	//get parent block
	parentBlock, err := nodectx.GetDbMgr().GetBlock(block.PrevBlockId, false, user.nodename)
	if err != nil {
		return err
	}

	//valid block with parent block
	valid, err := IsBlockValid(block, parentBlock)
	if !valid {
		return err
	}

	//search cache, gather all blocks can be connected with this block
	blocks, err := nodectx.GetDbMgr().GatherBlocksFromCache(block, true, user.nodename)
	if err != nil {
		return err
	}

	//get all trxs from those blocks
	var trxs []*quorumpb.Trx
	trxs, err = GetAllTrxs(blocks)
	if err != nil {
		return err
	}

	//apply those trxs
	err = user.applyTrxs(trxs, user.nodename)
	if err != nil {
		return err
	}

	//move gathered blocks from cache to chain
	for _, block := range blocks {
		molauser_log.Infof("Move block %s from cache to normal", block.BlockId)
		err := nodectx.GetDbMgr().AddBlock(block, false, user.nodename)
		if err != nil {
			return err
		}

		err = nodectx.GetDbMgr().RmBlock(block.BlockId, true, user.nodename)
		if err != nil {
			return err
		}
	}

	//calculate new height
	molauser_log.Debugf("height before recal %d", user.grpItem.HighestHeight)
	newHeight, newHighestBlockId, err := RecalChainHeight(blocks, user.grpItem.HighestHeight, user.nodename)
	molauser_log.Debugf("new height %d, new highest blockId %v", newHeight, newHighestBlockId)

	//if the new block is not highest block after recalculate, we need to "trim" the chain
	if newHeight < user.grpItem.HighestHeight {

		//from parent of the new blocks, get all blocks not belong to the longest path
		resendBlocks, err := GetTrimedBlocks(blocks, user.nodename)
		if err != nil {
			return err
		}

		var resendTrxs []*quorumpb.Trx
		resendTrxs, err = GetMyTrxs(resendBlocks, user.nodename, user.grpItem.UserSignPubkey)

		if err != nil {
			return err
		}

		UpdateResendCount(resendTrxs)
		err = user.resendTrx(resendTrxs)
	}

	return user.cIface.UpdChainInfo(newHeight, newHighestBlockId)
}

//resend all trx in the list
func (user *MolassesUser) resendTrx(trxs []*quorumpb.Trx) error {
	molauser_log.Infof("resendTrx")
	for _, trx := range trxs {
		molauser_log.Infof("Resend Trx %s", trx.TrxId)
		user.cIface.GetProducerTrxMgr().ResendTrx(trx)
	}
	return nil
}

func (user *MolassesUser) applyTrxs(trxs []*quorumpb.Trx, nodename string) error {
	molauser_log.Infof("applyTrxs called")
	for _, trx := range trxs {
		//check if trx already applied
		isExist, err := nodectx.GetDbMgr().IsTrxExist(trx.TrxId, nodename)
		if err != nil {
			molauser_log.Infof(err.Error())
			continue
		}

		if isExist {
			molauser_log.Infof("Trx %s existed, update trx only", trx.TrxId)
			nodectx.GetDbMgr().AddTrx(trx)
			continue
		}

		//new trx, apply it
		if trx.Type == quorumpb.TrxType_POST && user.grpItem.EncryptType == quorumpb.GroupEncryptType_PRIVATE {
			//for post, private group, encrypted by pgp for all announced group user
			ks := localcrypto.GetKeystore()
			decryptData, err := ks.Decrypt(user.grpItem.UserEncryptPubkey, trx.Data)
			if err != nil {
				return err
			}
			trx.Data = decryptData
		} else {
			//decode trx data
			ciperKey, err := hex.DecodeString(user.grpItem.CipherKey)
			if err != nil {
				return err
			}

			decryptData, err := localcrypto.AesDecode(trx.Data, ciperKey)
			if err != nil {
				return err
			}

			trx.Data = decryptData
		}

		molauser_log.Infof("try apply trx %s", trx.TrxId)
		//apply trx content
		switch trx.Type {
		case quorumpb.TrxType_POST:
			molauser_log.Infof("Apply POST trx")
			nodectx.GetDbMgr().AddPost(trx, nodename)
		case quorumpb.TrxType_AUTH:
			molauser_log.Infof("Apply AUTH trx")
			nodectx.GetDbMgr().UpdateBlkListItem(trx, nodename)
		case quorumpb.TrxType_PRODUCER:
			molauser_log.Infof("Apply PRODUCER Trx")
			nodectx.GetDbMgr().UpdateProducer(trx, nodename)
			user.cIface.UpdProducerList()
			user.cIface.UpdProducer()
		case quorumpb.TrxType_ANNOUNCE:
			molauser_log.Infof("Apply ANNOUNCE trx")
			nodectx.GetDbMgr().UpdateAnnounce(trx, nodename)
		case quorumpb.TrxType_SCHEMA:
			molauser_log.Infof("Apply SCHEMA trx ")
			nodectx.GetDbMgr().UpdateSchema(trx, nodename)
		default:
			molauser_log.Infof("Unsupported msgType %s", trx.Type)
		}

		//save trx to db
		nodectx.GetDbMgr().AddTrx(trx, nodename)
	}

	return nil
}
