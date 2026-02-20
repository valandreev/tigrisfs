using System;
using System.Drawing;
using System.Runtime.InteropServices;
using SharpShell.Attributes;
using SharpShell.Interop;
using SharpShell.SharpIconOverlayHandler;

namespace TigrisFS.ExplorerExtension
{
    internal static class OverlayStatusRules
    {
        public static bool TryGetStatus(string path, out IntegrationPathStatus status)
        {
            status = null;
            if (!IntegrationClient.TryGetPathStatus(path, out var current))
            {
                return false;
            }

            if (current == null || !current.Mounted || !current.Exists)
            {
                return false;
            }

            status = current;
            return true;
        }

        public static bool IsSyncing(IntegrationPathStatus status)
        {
            return status.Loading || status.Dirty || status.DownloadBps > 1 || status.UploadBps > 1;
        }

        public static bool IsCached(IntegrationPathStatus status)
        {
            return status.FullyCached || status.CachedBytes > 0;
        }
    }

    [ComVisible(true)]
    [Guid("7E58D6B9-6F75-4A44-B56E-3E4DA5039661")]
    [DisplayName("TigrisFS Pinned Overlay")]
    [RegistrationName("0TigrisFSPinnedOverlay")]
    [COMServerAssociation(AssociationType.AllFilesAndFolders)]
    public class TigrisPinnedOverlayHandler : SharpIconOverlayHandler
    {
        protected override int GetPriority()
        {
            return 0;
        }

        protected override bool CanShowOverlay(string path, FILE_ATTRIBUTE attributes)
        {
            return OverlayStatusRules.TryGetStatus(path, out var status) && status.Pinned;
        }

        protected override Icon GetOverlayIcon()
        {
            return OverlayIcons.Pinned;
        }
    }

    [ComVisible(true)]
    [Guid("7E7AB25A-4ACF-48A5-88F8-CE8B06C5D95C")]
    [DisplayName("TigrisFS Syncing Overlay")]
    [RegistrationName("1TigrisFSSyncingOverlay")]
    [COMServerAssociation(AssociationType.AllFilesAndFolders)]
    public class TigrisSyncingOverlayHandler : SharpIconOverlayHandler
    {
        protected override int GetPriority()
        {
            return 1;
        }

        protected override bool CanShowOverlay(string path, FILE_ATTRIBUTE attributes)
        {
            return OverlayStatusRules.TryGetStatus(path, out var status) &&
                   !status.Pinned &&
                   OverlayStatusRules.IsSyncing(status);
        }

        protected override Icon GetOverlayIcon()
        {
            return OverlayIcons.Syncing;
        }
    }

    [ComVisible(true)]
    [Guid("7A08AC5D-C713-41F0-B0AB-01984B6D547A")]
    [DisplayName("TigrisFS Cached Overlay")]
    [RegistrationName("2TigrisFSCachedOverlay")]
    [COMServerAssociation(AssociationType.AllFilesAndFolders)]
    public class TigrisCachedOverlayHandler : SharpIconOverlayHandler
    {
        protected override int GetPriority()
        {
            return 2;
        }

        protected override bool CanShowOverlay(string path, FILE_ATTRIBUTE attributes)
        {
            return OverlayStatusRules.TryGetStatus(path, out var status) &&
                   !status.Pinned &&
                   !OverlayStatusRules.IsSyncing(status) &&
                   OverlayStatusRules.IsCached(status);
        }

        protected override Icon GetOverlayIcon()
        {
            return OverlayIcons.Cached;
        }
    }
}
