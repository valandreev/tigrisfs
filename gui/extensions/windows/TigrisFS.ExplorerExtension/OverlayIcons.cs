using System.Drawing;

namespace TigrisFS.ExplorerExtension
{
    internal static class OverlayIcons
    {
        public static Icon Pinned { get; } = CreateIcon(Color.FromArgb(220, 21, 87, 36), "P");
        public static Icon Syncing { get; } = CreateIcon(Color.FromArgb(220, 0, 102, 204), "S");
        public static Icon Cached { get; } = CreateIcon(Color.FromArgb(220, 153, 102, 0), "C");

        private static Icon CreateIcon(Color color, string glyph)
        {
            var bmp = new Bitmap(32, 32);
            using (var g = Graphics.FromImage(bmp))
            {
                g.Clear(Color.Transparent);
                g.SmoothingMode = System.Drawing.Drawing2D.SmoothingMode.AntiAlias;

                using (var brush = new SolidBrush(color))
                {
                    g.FillEllipse(brush, 2, 2, 28, 28);
                }

                using (var pen = new Pen(Color.White, 2))
                {
                    g.DrawEllipse(pen, 2, 2, 28, 28);
                }

                using (var font = new Font("Segoe UI", 14, FontStyle.Bold, GraphicsUnit.Pixel))
                using (var textBrush = new SolidBrush(Color.White))
                {
                    var size = g.MeasureString(glyph, font);
                    var x = (bmp.Width - size.Width) / 2f;
                    var y = (bmp.Height - size.Height) / 2f - 1;
                    g.DrawString(glyph, font, textBrush, x, y);
                }
            }

            return Icon.FromHandle(bmp.GetHicon());
        }
    }
}
