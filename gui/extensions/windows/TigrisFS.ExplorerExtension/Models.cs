using System.Runtime.Serialization;

namespace TigrisFS.ExplorerExtension
{
    [DataContract]
    internal sealed class IntegrationServerState
    {
        [DataMember(Name = "address")]
        public string Address { get; set; }

        [DataMember(Name = "token")]
        public string Token { get; set; }
    }

    [DataContract]
    internal sealed class IntegrationPathStatus
    {
        [DataMember(Name = "path")]
        public string Path { get; set; }

        [DataMember(Name = "mounted")]
        public bool Mounted { get; set; }

        [DataMember(Name = "exists")]
        public bool Exists { get; set; }

        [DataMember(Name = "is_dir")]
        public bool IsDir { get; set; }

        [DataMember(Name = "pinned")]
        public bool Pinned { get; set; }

        [DataMember(Name = "cached_bytes")]
        public ulong CachedBytes { get; set; }

        [DataMember(Name = "fully_cached")]
        public bool FullyCached { get; set; }

        [DataMember(Name = "loading")]
        public bool Loading { get; set; }

        [DataMember(Name = "dirty")]
        public bool Dirty { get; set; }

        [DataMember(Name = "download_bps")]
        public double DownloadBps { get; set; }

        [DataMember(Name = "upload_bps")]
        public double UploadBps { get; set; }
    }

    [DataContract]
    internal sealed class IntegrationPathStatusResponse
    {
        [DataMember(Name = "success")]
        public bool Success { get; set; }

        [DataMember(Name = "status")]
        public IntegrationPathStatus Status { get; set; }

        [DataMember(Name = "message")]
        public string Message { get; set; }
    }

    [DataContract]
    internal sealed class IntegrationCommandRequest
    {
        [DataMember(Name = "action")]
        public string Action { get; set; }

        [DataMember(Name = "path", EmitDefaultValue = false)]
        public string Path { get; set; }

        [DataMember(Name = "recursive", EmitDefaultValue = false)]
        public bool? Recursive { get; set; }
    }

    [DataContract]
    internal sealed class IntegrationCommandResponse
    {
        [DataMember(Name = "success")]
        public bool Success { get; set; }

        [DataMember(Name = "message")]
        public string Message { get; set; }
    }
}
