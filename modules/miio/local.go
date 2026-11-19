package miio

import (
	"context"
	"fmt"

	"github.com/Yonsm/misgo/local"
)

type localDevice struct {
	service *local.Service
}

func newLocal(host, token string) (*localDevice, error) {
	s, err := local.New(host, token)
	if err != nil {
		return nil, err
	}
	return &localDevice{service: s}, nil
}

func (d *localDevice) read(ctx context.Context, ids []iid) ([]any, error) {
	if ids[0].name != "" {
		return d.service.GetProps(ctx, legacyNames(ids))
	}
	iids := make([]local.IID, len(ids))
	for i, id := range ids {
		iids[i] = local.IID{SIID: id.siid, PIID: id.piid}
	}
	return d.service.MIoTGetProps(ctx, iids)
}

func (d *localDevice) set(ctx context.Context, id iid, val any) error {
	if id.name != "" {
		vals, err := d.service.SetProps(ctx, []local.Property{{Name: id.name, Value: val}})
		if err != nil {
			return err
		}
		if len(vals) != 0 && vals[0] == "ok" {
			return nil
		}
	} else {
		codes, err := d.service.MIoTSetProps(ctx, []local.MIoTProperty{{
			IID: local.IID{SIID: id.siid, PIID: id.piid}, Value: val,
		}})
		if err != nil {
			return err
		}
		if len(codes) != 0 && codes[0] == 0 {
			return nil
		}
	}
	return fmt.Errorf("MiIO 写入 %s 未获确认", id)
}
