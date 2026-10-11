// Disposable native window for acceptance testing. No user documents are opened.
using System;
using System.Drawing;
using System.IO;
using System.Windows.Forms;

class Fixture : Form {
    int count = 0;
    Label result = new Label();
    TextBox input = new TextBox();
    protected override bool ShowWithoutActivation { get { return true; } }
    public Fixture(string output) {
        Text = "DotControl acceptance fixture";
        Size = new Size(620, 420);
        StartPosition = FormStartPosition.Manual;
        Location = new Point(40, 80);
        Label heading = new Label { Text="DotControl — disposable test window", AutoSize=true, Location=new Point(20,20) };
        input.AccessibleName="DotControl test input";
        input.Location=new Point(20,60);input.Size=new Size(555,140);input.Multiline=true;
        Button increment=new Button { Text="Increment", AccessibleName="Increment test counter", Location=new Point(20,220), Size=new Size(150,40) };
        increment.Click+=(s,e)=>{ count++;result.Text="Count: "+count;result.AccessibleName=result.Text; };
        Button save=new Button { Text="Save test text",AccessibleName="Save test text",Location=new Point(190,220),Size=new Size(160,40) };
        save.Click+=(s,e)=>{ File.WriteAllText(output,input.Text,new System.Text.UTF8Encoding(false));result.Text="Saved test text";result.AccessibleName=result.Text; };
        result.Text="Count: 0";result.AccessibleName=result.Text;result.Location=new Point(20,285);result.AutoSize=true;
        Controls.AddRange(new Control[]{heading,input,increment,save,result});
    }
    [STAThread] static void Main(string[] args) {
        if(args.Length!=1)throw new ArgumentException("Explicit disposable output path required");
        Application.EnableVisualStyles();Application.Run(new Fixture(Path.GetFullPath(args[0])));
    }
}
