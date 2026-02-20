using System;
using System.Collections.Concurrent;
using System.IO;
using System.Net;
using System.Runtime.Serialization.Json;

namespace TigrisFS.ExplorerExtension
{
    internal static class IntegrationClient
    {
        private static readonly ConcurrentDictionary<string, CacheEntry> StatusCache = new ConcurrentDictionary<string, CacheEntry>(StringComparer.OrdinalIgnoreCase);
        private static readonly TimeSpan StatusTtl = TimeSpan.FromSeconds(2);
        private static readonly TimeSpan RequestTimeout = TimeSpan.FromMilliseconds(800);

        private sealed class CacheEntry
        {
            public IntegrationPathStatus Status;
            public DateTime ExpiresAtUtc;
        }

        public static bool TryGetPathStatus(string path, out IntegrationPathStatus status)
        {
            status = null;
            var normalized = NormalizePath(path);
            if (string.IsNullOrWhiteSpace(normalized))
            {
                return false;
            }

            if (StatusCache.TryGetValue(normalized, out var cached) && cached.ExpiresAtUtc > DateTime.UtcNow)
            {
                status = cached.Status;
                return true;
            }

            if (!TryReadServerState(out var state))
            {
                return false;
            }

            var encodedPath = Uri.EscapeDataString(normalized);
            var url = $"http://{state.Address}/v1/path/status?path={encodedPath}";
            if (!TryRequest<IntegrationPathStatusResponse>(url, "GET", state.Token, null, out var response))
            {
                return false;
            }

            if (!response.Success || response.Status == null)
            {
                return false;
            }

            status = response.Status;
            StatusCache[normalized] = new CacheEntry
            {
                Status = status,
                ExpiresAtUtc = DateTime.UtcNow.Add(StatusTtl),
            };
            return true;
        }

        public static bool SendCommand(string action, string path, bool? recursive)
        {
            if (!TryReadServerState(out var state))
            {
                return false;
            }

            var req = new IntegrationCommandRequest
            {
                Action = action,
                Path = path,
                Recursive = recursive,
            };

            var url = $"http://{state.Address}/v1/command";
            if (!TryRequest<IntegrationCommandResponse>(url, "POST", state.Token, req, out var response))
            {
                return false;
            }

            return response.Success;
        }

        private static bool TryReadServerState(out IntegrationServerState state)
        {
            state = null;

            var appData = Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData);
            if (string.IsNullOrWhiteSpace(appData))
            {
                return false;
            }

            var stateFile = Path.Combine(appData, "TigrisFS", "integration_server.json");
            if (!File.Exists(stateFile))
            {
                return false;
            }

            try
            {
                using (var fs = File.OpenRead(stateFile))
                {
                    state = Deserialize<IntegrationServerState>(fs);
                }
            }
            catch
            {
                state = null;
                return false;
            }

            return state != null && !string.IsNullOrWhiteSpace(state.Address) && !string.IsNullOrWhiteSpace(state.Token);
        }

        private static bool TryRequest<TResponse>(
            string url,
            string method,
            string token,
            object requestBody,
            out TResponse response
        ) where TResponse : class
        {
            response = null;

            var request = (HttpWebRequest)WebRequest.Create(url);
            request.Method = method;
            request.Timeout = (int)RequestTimeout.TotalMilliseconds;
            request.ReadWriteTimeout = (int)RequestTimeout.TotalMilliseconds;
            request.Headers["X-TigrisFS-Token"] = token;

            if (requestBody != null)
            {
                request.ContentType = "application/json";
                try
                {
                    using (var stream = request.GetRequestStream())
                    {
                        Serialize(stream, requestBody);
                    }
                }
                catch
                {
                    return false;
                }
            }

            try
            {
                using (var httpResponse = (HttpWebResponse)request.GetResponse())
                {
                    if ((int)httpResponse.StatusCode < 200 || (int)httpResponse.StatusCode > 299)
                    {
                        return false;
                    }

                    using (var stream = httpResponse.GetResponseStream())
                    {
                        if (stream == null)
                        {
                            return false;
                        }
                        response = Deserialize<TResponse>(stream);
                    }
                }

                return response != null;
            }
            catch
            {
                return false;
            }
        }

        private static void Serialize(Stream stream, object obj)
        {
            var serializer = new DataContractJsonSerializer(obj.GetType());
            serializer.WriteObject(stream, obj);
        }

        private static T Deserialize<T>(Stream stream) where T : class
        {
            var serializer = new DataContractJsonSerializer(typeof(T));
            return serializer.ReadObject(stream) as T;
        }

        private static string NormalizePath(string path)
        {
            if (string.IsNullOrWhiteSpace(path))
            {
                return string.Empty;
            }

            var trimmed = path.Trim();
            try
            {
                return Path.GetFullPath(trimmed);
            }
            catch
            {
                return trimmed;
            }
        }
    }
}
