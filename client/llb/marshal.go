package llb

import (
	"io"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/solver/pb"
	digest "github.com/opencontainers/go-digest"
	"google.golang.org/protobuf/proto"
)

// Definition is the LLB definition structure with per-vertex metadata entries
// Corresponds to the Definition structure defined in solver/pb.Definition.
type Definition struct {
	Def         [][]byte
	Metadata    map[digest.Digest]OpMetadata
	Source      *pb.Source
	Constraints *Constraints
}

func (def *Definition) ToPB() *pb.Definition {
	metas := make(map[string]*pb.OpMetadata, len(def.Metadata))
	for dgst, meta := range def.Metadata {
		metas[string(dgst)] = meta.ToPB()
	}
	return &pb.Definition{
		Def:      def.Def,
		Source:   def.Source,
		Metadata: metas,
	}
}

func (def *Definition) FromPB(x *pb.Definition) {
	def.Def = x.Def
	def.Source = x.Source
	def.Metadata = make(map[digest.Digest]OpMetadata, len(x.Metadata))
	for dgst, meta := range x.Metadata {
		def.Metadata[digest.Digest(dgst)] = NewOpMetadata(meta)
	}
}

func (def *Definition) Head() (digest.Digest, error) {
	if len(def.Def) == 0 {
		return "", nil
	}

	last := def.Def[len(def.Def)-1]

	var pop pb.Op
	if err := proto.Unmarshal(last, &pop); err != nil {
		return "", err
	}
	if len(pop.Inputs) == 0 {
		return "", nil
	}

	return digest.Digest(pop.Inputs[0].Digest), nil
}

func WriteTo(def *Definition, w io.Writer) error {
	b, err := def.ToPB().Marshal()
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

func ReadFrom(r io.Reader) (*Definition, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var pbDef pb.Definition
	if err := proto.Unmarshal(b, &pbDef); err != nil {
		return nil, err
	}
	var def Definition
	def.FromPB(&pbDef)
	return &def, nil
}

func MarshalConstraints(base, override *Constraints) (*pb.Op, *pb.OpMetadata) {
	c := *base
	c.WorkerConstraints = append([]string{}, c.WorkerConstraints...)

	if p := override.Platform; p != nil {
		c.Platform = p
	}

	c.WorkerConstraints = append(c.WorkerConstraints, override.WorkerConstraints...)
	c.Metadata = mergeMetadata(c.Metadata, override.Metadata)

	if c.Platform == nil {
		defaultPlatform := platforms.Normalize(platforms.DefaultSpec())
		c.Platform = &defaultPlatform
	}

	opPlatform := pb.Platform{
		OS:           c.Platform.OS,
		Architecture: c.Platform.Architecture,
		Variant:      c.Platform.Variant,
		OSVersion:    c.Platform.OSVersion,
	}
	if c.Platform.OSFeatures != nil {
		opPlatform.OSFeatures = append([]string{}, c.Platform.OSFeatures...)
	}

	return &pb.Op{
		Platform: &opPlatform,
		Constraints: &pb.WorkerConstraints{
			Filter: c.WorkerConstraints,
		},
	}, c.Metadata.ToPB()
}

type MarshalCache struct {
	digest      digest.Digest
	dt          []byte
	md          *pb.OpMetadata
	srcs        []*SourceLocation
	constraints *Constraints
}

func (mc *MarshalCache) Cached(c *Constraints) bool {
	return mc.dt != nil && mc.constraints == c
}

func (mc *MarshalCache) Load() (digest.Digest, []byte, *pb.OpMetadata, []*SourceLocation, error) {
	return mc.digest, mc.dt, mc.md, mc.srcs, nil
}

func (mc *MarshalCache) Store(dt []byte, md *pb.OpMetadata, srcs []*SourceLocation, c *Constraints) {
	mc.digest = digest.FromBytes(dt)
	mc.dt = dt
	mc.md = md
	mc.constraints = c
	mc.srcs = srcs
}
