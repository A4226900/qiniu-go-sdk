package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/qiniu/go-sdk/v7/internal/hostprovider"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/qiniu/go-sdk/v7/client"
)

// ResumeUploader 表示一个分片上传的对象
type ResumeUploader struct {
	Client *client.Client
	Cfg    *Config
}

// NewResumeUploader 表示构建一个新的分片上传的对象
func NewResumeUploader(cfg *Config) *ResumeUploader {
	return NewResumeUploaderEx(cfg, nil)
}

// NewResumeUploaderEx 表示构建一个新的分片上传的对象
func NewResumeUploaderEx(cfg *Config, clt *client.Client) *ResumeUploader {
	if cfg == nil {
		cfg = &Config{}
	}

	if clt == nil {
		clt = &client.DefaultClient
	}

	return &ResumeUploader{
		Client: clt,
		Cfg:    cfg,
	}
}

// Put 方法用来上传一个文件，支持断点续传和分块上传。
//
// ctx     是请求的上下文。
// ret     是上传成功后返回的数据。如果 upToken 中没有设置 CallbackUrl 或 ReturnBody，那么返回的数据结构是 PutRet 结构。
// upToken 是由业务服务器颁发的上传凭证。
// key     是要上传的文件访问路径。比如："foo/bar.jpg"。注意我们建议 key 不要以 '/' 开头。另外，key 为空字符串是合法的。
// f       是文件内容的访问接口。考虑到需要支持分块上传和断点续传，要的是 io.ReaderAt 接口，而不是 io.Reader。
// fsize   是要上传的文件大小。
// extra   是上传的一些可选项。详细见 RputExtra 结构的描述。
//
func (p *ResumeUploader) Put(ctx context.Context, ret interface{}, upToken string, key string, f io.ReaderAt, fsize int64, extra *RputExtra) error {
	return p.rput(ctx, ret, upToken, key, true, f, fsize, nil, extra)
}

func (p *ResumeUploader) PutWithoutSize(ctx context.Context, ret interface{}, upToken, key string, r io.Reader, extra *RputExtra) error {
	return p.rputWithoutSize(ctx, ret, upToken, key, true, r, extra)
}

// PutWithoutKey 方法用来上传一个文件，支持断点续传和分块上传。文件命名方式首先看看
// upToken 中是否设置了 saveKey，如果设置了 saveKey，那么按 saveKey 要求的规则生成 key，否则自动以文件的 hash 做 key。
//
// ctx     是请求的上下文。
// ret     是上传成功后返回的数据。如果 upToken 中没有设置 CallbackUrl 或 ReturnBody，那么返回的数据结构是 PutRet 结构。
// upToken 是由业务服务器颁发的上传凭证。
// f       是文件内容的访问接口。考虑到需要支持分块上传和断点续传，要的是 io.ReaderAt 接口，而不是 io.Reader。
// fsize   是要上传的文件大小。
// extra   是上传的一些可选项。详细见 RputExtra 结构的描述。
//
func (p *ResumeUploader) PutWithoutKey(ctx context.Context, ret interface{}, upToken string, f io.ReaderAt, fsize int64, extra *RputExtra) error {
	return p.rput(ctx, ret, upToken, "", false, f, fsize, nil, extra)
}

// PutWithoutKeyAndSize 方法用来上传一个文件，支持断点续传和分块上传。文件命名方式首先看看
// upToken 中是否设置了 saveKey，如果设置了 saveKey，那么按 saveKey 要求的规则生成 key，否则自动以文件的 hash 做 key。
//
// ctx     是请求的上下文。
// ret     是上传成功后返回的数据。如果 upToken 中没有设置 CallbackUrl 或 ReturnBody，那么返回的数据结构是 PutRet 结构。
// upToken 是由业务服务器颁发的上传凭证。
// f       是文件内容的访问接口。
// extra   是上传的一些可选项。详细见 RputExtra 结构的描述。
//
func (p *ResumeUploader) PutWithoutKeyAndSize(ctx context.Context, ret interface{}, upToken string, f io.Reader, extra *RputExtra) error {
	return p.rputWithoutSize(ctx, ret, upToken, "", false, f, extra)
}

// PutFile 用来上传一个文件，支持断点续传和分块上传。
// 和 Put 不同的只是一个通过提供文件路径来访问文件内容，一个通过 io.ReaderAt 来访问。
//
// ctx       是请求的上下文。
// ret       是上传成功后返回的数据。如果 upToken 中没有设置 CallbackUrl 或 ReturnBody，那么返回的数据结构是 PutRet 结构。
// upToken   是由业务服务器颁发的上传凭证。
// key       是要上传的文件访问路径。比如："foo/bar.jpg"。注意我们建议 key 不要以 '/' 开头。另外，key 为空字符串是合法的。
// localFile 是要上传的文件的本地路径。
// extra     是上传的一些可选项。详细见 RputExtra 结构的描述。
//
func (p *ResumeUploader) PutFile(ctx context.Context, ret interface{}, upToken, key, localFile string, extra *RputExtra) error {
	return p.rputFile(ctx, ret, upToken, key, true, localFile, extra)
}

// PutFileWithoutKey 上传一个文件，支持断点续传和分块上传。文件命名方式首先看看
// upToken 中是否设置了 saveKey，如果设置了 saveKey，那么按 saveKey 要求的规则生成 key，否则自动以文件的 hash 做 key。
// 和 PutWithoutKey 不同的只是一个通过提供文件路径来访问文件内容，一个通过 io.ReaderAt 来访问。
//
// ctx       是请求的上下文。
// ret       是上传成功后返回的数据。如果 upToken 中没有设置 CallbackUrl 或 ReturnBody，那么返回的数据结构是 PutRet 结构。
// upToken   是由业务服务器颁发的上传凭证。
// localFile 是要上传的文件的本地路径。
// extra     是上传的一些可选项。详细见 RputExtra 结构的描述。
//
func (p *ResumeUploader) PutFileWithoutKey(ctx context.Context, ret interface{}, upToken, localFile string, extra *RputExtra) error {
	return p.rputFile(ctx, ret, upToken, "", false, localFile, extra)
}

type fileDetailsInfo struct {
	fileFullPath string
	fileInfo     os.FileInfo
}

func (p *ResumeUploader) rput(ctx context.Context, ret interface{}, upToken string, key string, hasKey bool, f io.ReaderAt, fsize int64, fileDetails *fileDetailsInfo, extra *RputExtra) (err error) {
	if extra == nil {
		extra = &RputExtra{}
	}
	extra.init()

	var (
		accessKey, bucket, recorderKey string
		fileInfo                       os.FileInfo               = nil
		hostProvider                   hostprovider.HostProvider = nil
	)

	if accessKey, bucket, err = getAkBucketFromUploadToken(upToken); err != nil {
		return
	}

	if extra.UpHost != "" {
		hostProvider = hostprovider.NewWithHosts([]string{extra.getUpHost(p.Cfg.UseHTTPS)})
	} else {
		hostProvider, err = p.resumeUploaderAPIs().upHostProvider(accessKey, bucket)
		if err != nil {
			return
		}
	}

	if extra.Recorder != nil && fileDetails != nil {
		recorderKey = extra.Recorder.GenerateRecorderKey(
			[]string{accessKey, bucket, key, "v1", fileDetails.fileFullPath, strconv.FormatInt(1<<blockBits, 10)},
			fileDetails.fileInfo)
		fileInfo = fileDetails.fileInfo
	}

	return uploadByWorkers(
		newResumeUploaderImpl(p, key, hasKey, upToken, hostProvider, fileInfo, extra, ret, recorderKey),
		ctx, newSizedChunkReader(f, fsize, 1<<blockBits))
}

func (p *ResumeUploader) rputWithoutSize(ctx context.Context, ret interface{}, upToken string, key string, hasKey bool, r io.Reader, extra *RputExtra) (err error) {
	if extra == nil {
		extra = &RputExtra{}
	}
	extra.init()

	var (
		accessKey, bucket string
		hostProvider      hostprovider.HostProvider = nil
	)

	if accessKey, bucket, err = getAkBucketFromUploadToken(upToken); err != nil {
		return
	}

	if extra.UpHost != "" {
		hostProvider = hostprovider.NewWithHosts([]string{extra.getUpHost(p.Cfg.UseHTTPS)})
	} else {
		hostProvider, err = p.resumeUploaderAPIs().upHostProvider(accessKey, bucket)
		if err != nil {
			return
		}
	}

	return uploadByWorkers(
		newResumeUploaderImpl(p, key, hasKey, upToken, hostProvider, nil, extra, ret, ""),
		ctx, newUnsizedChunkReader(r, 1<<blockBits))
}

func (p *ResumeUploader) rputFile(ctx context.Context, ret interface{}, upToken string, key string, hasKey bool, localFile string, extra *RputExtra) (err error) {
	var (
		file        *os.File
		fileInfo    os.FileInfo
		fileDetails *fileDetailsInfo
	)

	if file, err = os.Open(localFile); err != nil {
		return
	}
	defer file.Close()

	if fileInfo, err = file.Stat(); err != nil {
		return
	}

	if fullPath, absErr := filepath.Abs(file.Name()); absErr == nil {
		fileDetails = &fileDetailsInfo{fileFullPath: fullPath, fileInfo: fileInfo}
	}

	return p.rput(ctx, ret, upToken, key, hasKey, file, fileInfo.Size(), fileDetails, extra)
}

// 创建块请求
func (p *ResumeUploader) Mkblk(ctx context.Context, upToken string, upHost string, ret *BlkputRet, blockSize int, body io.Reader, size int) error {
	return p.resumeUploaderAPIs().mkBlk(ctx, upToken, upHost, ret, int64(blockSize), body, int64(size))
}

// 发送bput请求
func (p *ResumeUploader) Bput(ctx context.Context, upToken string, ret *BlkputRet, body io.Reader, size int) error {
	return p.resumeUploaderAPIs().bput(ctx, upToken, ret, body, int64(size))
}

// 创建文件请求
func (p *ResumeUploader) Mkfile(ctx context.Context, upToken string, upHost string, ret interface{}, key string, hasKey bool, fsize int64, extra *RputExtra) (err error) {
	return p.resumeUploaderAPIs().mkfile(ctx, upToken, upHost, ret, key, hasKey, fsize, extra)
}

func (p *ResumeUploader) UpHost(ak, bucket string) (upHost string, err error) {
	return p.resumeUploaderAPIs().upHost(ak, bucket)
}

func (p *ResumeUploader) resumeUploaderAPIs() *resumeUploaderAPIs {
	return &resumeUploaderAPIs{Client: p.Client, Cfg: p.Cfg}
}

type (
	// 用于实现 resumeUploaderBase 的 V1 分片接口
	resumeUploaderImpl struct {
		client         *client.Client
		cfg            *Config
		key            string
		hasKey         bool
		upToken        string
		upHostProvider hostprovider.HostProvider
		bufPool        *sync.Pool
		extra          *RputExtra
		ret            interface{}
		fileSize       int64
		fileInfo       os.FileInfo
		recorderKey    string
		lock           sync.Mutex
	}

	resumeUploaderRecoveryInfoContext struct {
		Ctx       string `json:"c"`
		Idx       int    `json:"i"`
		ChunkSize int    `json:"s"`
		Offset    int64  `json:"o"`
		ExpiredAt int64  `json:"e"`
	}

	resumeUploaderRecoveryInfo struct {
		FileSize     int64                               `json:"s"`
		ModTimeStamp int64                               `json:"m"`
		Contexts     []resumeUploaderRecoveryInfoContext `json:"c"`
	}
)

func newResumeUploaderImpl(resumeUploader *ResumeUploader, key string, hasKey bool, upToken string, upHostProvider hostprovider.HostProvider, fileInfo os.FileInfo, extra *RputExtra, ret interface{}, recorderKey string) *resumeUploaderImpl {
	return &resumeUploaderImpl{
		client:         resumeUploader.Client,
		cfg:            resumeUploader.Cfg,
		key:            key,
		hasKey:         hasKey,
		upToken:        upToken,
		upHostProvider: upHostProvider,
		bufPool: &sync.Pool{
			New: func() interface{} {
				return bytes.NewBuffer(make([]byte, 0, extra.ChunkSize))
			},
		},
		extra:       extra,
		ret:         ret,
		fileSize:    0,
		fileInfo:    fileInfo,
		recorderKey: recorderKey,
	}
}

func (impl *resumeUploaderImpl) initUploader(ctx context.Context) ([]int64, error) {
	var recovered []int64
	if impl.extra.Recorder != nil {
		if recorderData, err := impl.extra.Recorder.Get(impl.recorderKey); err == nil {
			recovered = impl.recover(ctx, recorderData)
		}
	}
	return recovered, nil
}

func (impl *resumeUploaderImpl) uploadChunk(ctx context.Context, c chunk) error {
	type ChunkRange struct {
		From int64
		Size int64
	}
	var (
		chunkSize      = int64(impl.extra.ChunkSize)
		apis           = impl.resumeUploaderAPIs()
		chunkRange     ChunkRange
		blkPutRet      BlkputRet
		err            error
		realChunkSize  int64
		totalChunkSize = int64(0)
		buffer         = impl.bufPool.Get().(*bytes.Buffer)
	)
	defer impl.bufPool.Put(buffer)

	for chunkOffset := int64(0); chunkOffset < c.size; chunkOffset += chunkRange.Size {
		chunkRange = ChunkRange{From: chunkOffset, Size: c.size - chunkOffset}
		if chunkRange.Size > chunkSize {
			chunkRange.Size = chunkSize
		}

		hash32 := crc32.NewIEEE()
		buffer.Reset()
		realChunkSize, err = io.Copy(hash32, io.TeeReader(io.NewSectionReader(c.reader, chunkRange.From, chunkRange.Size), buffer))
		if err != nil {
			impl.extra.NotifyErr(int(c.id), int(c.size), err)
			return err
		} else if realChunkSize == 0 {
			break
		} else {
			totalChunkSize += realChunkSize
		}
		crc32Value := hash32.Sum32()

		seekableData := bytes.NewReader(buffer.Bytes())
		if chunkOffset == 0 {
			err = doUploadAction(impl.upHostProvider, impl.extra.TryTimes, impl.extra.HostFreezeDuration, func(host string) error {
				if _, sErr := seekableData.Seek(0, io.SeekStart); sErr != nil {
					return sErr
				}

				if e := apis.mkBlk(ctx, impl.upToken, host, &blkPutRet, c.size, seekableData, realChunkSize); e != nil {
					return e
				}
				if blkPutRet.Crc32 != crc32Value || int64(blkPutRet.Offset) != chunkOffset+realChunkSize {
					return ErrUnmatchedChecksum
				}
				return nil
			})
		} else {
			err = doUploadAction(impl.upHostProvider, impl.extra.TryTimes, impl.extra.HostFreezeDuration, func(host string) error {
				blkPutRet.Host = host
				if _, sErr := seekableData.Seek(0, io.SeekStart); sErr != nil {
					return sErr
				}

				if e := apis.bput(ctx, impl.upToken, &blkPutRet, seekableData, realChunkSize); e != nil {
					return e
				}
				if blkPutRet.Crc32 != crc32Value || int64(blkPutRet.Offset) != chunkOffset+realChunkSize {
					return ErrUnmatchedChecksum
				}
				return nil
			})
		}

		if err != nil {
			impl.extra.NotifyErr(int(c.id), int(realChunkSize), err)
			return err
		}
	}

	blkPutRet.blkIdx = int(c.id)
	blkPutRet.fileOffset = c.offset
	blkPutRet.chunkSize = int(totalChunkSize)
	impl.extra.Notify(blkPutRet.blkIdx, int(totalChunkSize), &blkPutRet)

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	func() {
		impl.lock.Lock()
		defer impl.lock.Unlock()
		impl.extra.Progresses = append(impl.extra.Progresses, blkPutRet)
		impl.fileSize += c.size
		impl.save(ctx)
	}()

	return nil
}

func (impl *resumeUploaderImpl) final(ctx context.Context) error {
	if impl.extra.Recorder != nil {
		impl.extra.Recorder.Delete(impl.recorderKey)
	}

	sort.Sort(blkputRets(impl.extra.Progresses))
	return doUploadAction(impl.upHostProvider, impl.extra.TryTimes, impl.extra.HostFreezeDuration, func(host string) error {
		return impl.resumeUploaderAPIs().mkfile(ctx, impl.upToken, host, impl.ret, impl.key, impl.hasKey, impl.fileSize, impl.extra)
	})
}

func (impl *resumeUploaderImpl) recover(ctx context.Context, recoverData []byte) (recovered []int64) {
	var recoveryInfo resumeUploaderRecoveryInfo
	if err := json.Unmarshal(recoverData, &recoveryInfo); err != nil {
		return
	}
	if impl.fileInfo == nil || recoveryInfo.FileSize != impl.fileInfo.Size() || recoveryInfo.ModTimeStamp != impl.fileInfo.ModTime().UnixNano() {
		return
	}

	for _, c := range recoveryInfo.Contexts {
		if time.Now().Before(time.Unix(c.ExpiredAt, 0)) {
			impl.fileSize += int64(c.ChunkSize)
			impl.extra.Progresses = append(impl.extra.Progresses, BlkputRet{
				blkIdx: c.Idx, fileOffset: c.Offset, chunkSize: c.ChunkSize, Ctx: c.Ctx, ExpiredAt: c.ExpiredAt,
			})
			recovered = append(recovered, int64(c.Offset))
		}
	}

	return
}

func (impl *resumeUploaderImpl) save(ctx context.Context) {
	var (
		recoveryInfo  resumeUploaderRecoveryInfo
		recoveredData []byte
		err           error
	)

	if impl.fileInfo == nil || impl.extra.Recorder == nil {
		return
	}

	recoveryInfo.FileSize = impl.fileInfo.Size()
	recoveryInfo.ModTimeStamp = impl.fileInfo.ModTime().UnixNano()
	recoveryInfo.Contexts = make([]resumeUploaderRecoveryInfoContext, 0, len(impl.extra.Progresses))

	for _, progress := range impl.extra.Progresses {
		recoveryInfo.Contexts = append(recoveryInfo.Contexts, resumeUploaderRecoveryInfoContext{
			Ctx: progress.Ctx, Idx: progress.blkIdx, Offset: progress.fileOffset, ChunkSize: progress.chunkSize, ExpiredAt: progress.ExpiredAt,
		})
	}

	if recoveredData, err = json.Marshal(recoveryInfo); err != nil {
		return
	}

	err = impl.extra.Recorder.Set(impl.recorderKey, recoveredData)
}

func (impl *resumeUploaderImpl) resumeUploaderAPIs() *resumeUploaderAPIs {
	return &resumeUploaderAPIs{Client: impl.client, Cfg: impl.cfg}
}
