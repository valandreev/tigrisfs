using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Runtime.InteropServices;
using System.Windows.Forms;
using SharpShell.Attributes;
using SharpShell.SharpContextMenu;

namespace TigrisFS.ExplorerExtension
{
    [ComVisible(true)]
    [Guid("8A7BBF3D-9F0D-4B50-9DB0-8F3173151AA4")]
    [DisplayName("TigrisFS Context Menu")]
    [RegistrationName("TigrisFSContextMenu")]
    [COMServerAssociation(AssociationType.AllFilesAndFolders)]
    public class TigrisContextMenuHandler : SharpContextMenu
    {
        protected override bool CanShowMenu()
        {
            var paths = SelectedPaths();
            if (paths.Count == 0)
            {
                return false;
            }

            foreach (var path in paths)
            {
                if (IntegrationClient.TryGetPathStatus(path, out var status) && status != null && status.Mounted)
                {
                    return true;
                }
            }

            return false;
        }

        protected override ContextMenuStrip CreateMenu()
        {
            var menu = new ContextMenuStrip();

            var pin = new ToolStripMenuItem("TigrisFS: Pin in Cache");
            pin.Click += (sender, args) => RunCommandForSelection("pin");
            menu.Items.Add(pin);

            var unpin = new ToolStripMenuItem("TigrisFS: Unpin from Cache");
            unpin.Click += (sender, args) => RunCommandForSelection("unpin");
            menu.Items.Add(unpin);

            menu.Items.Add(new ToolStripSeparator());

            var unmount = new ToolStripMenuItem("TigrisFS: Unmount Mount");
            unmount.Click += (sender, args) => RunCommandForSelection("unmount");
            menu.Items.Add(unmount);

            var show = new ToolStripMenuItem("TigrisFS: Open Dashboard");
            show.Click += (sender, args) => IntegrationClient.SendCommand("show", null, null);
            menu.Items.Add(show);

            return menu;
        }

        private void RunCommandForSelection(string action)
        {
            var paths = SelectedPaths();
            if (paths.Count == 0)
            {
                return;
            }

            foreach (var path in paths)
            {
                bool? recursive = null;
                if (action == "pin" || action == "unpin")
                {
                    recursive = Directory.Exists(path);
                }

                IntegrationClient.SendCommand(action, path, recursive);
            }
        }

        private List<string> SelectedPaths()
        {
            return SelectedItemPaths?
                .Where(p => !string.IsNullOrWhiteSpace(p))
                .Select(p => p.Trim())
                .Distinct(System.StringComparer.OrdinalIgnoreCase)
                .ToList() ?? new List<string>();
        }
    }
}
