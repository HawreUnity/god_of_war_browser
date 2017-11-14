$(document).ready(function() {
    var data3d = $('.view-item-container');
    gwInitRenderer(data3d);
    gr_instance.setInterfaceCameraMode(true);

    var url = new URL(document.location);
    var packfile = url.searchParams.get('f');
    var flpid = url.searchParams.get('r');
    var commands = JSON.parse(url.searchParams.get('c'));

    $.getJSON('/json/pack/' + packfile + '/' + flpid, function(resp) {
        var flp = resp.Data;
        var flpdata = flp.FLP;

        var font = undefined;
        var x = 0;
        var y = 0;
        var fontscale = 1.0;

        var matmap = {};

        for (var iCmd in commands) {
            var cmd = commands[iCmd];

            if (cmd.Flags & 8) {
                font = flpdata.Fonts[flpdata.GlobalHandlersIndexes[cmd.FontHandler].IdInThatTypeArray];
                fontscale = cmd.FontScale;
            }
            if (cmd.Flags & 2) {
                x = cmd.OffsetX / 16;
            }
            if (cmd.Flags & 1) {
                y = cmd.OffsetY / 16;
            }

            for (var iGlyph in cmd.Glyphs) {
                var glyph = cmd.Glyphs[iGlyph];

                console.log(x, glyph);

                var flagsdatas2 = ((!!font.Flag4Datas2) ? font.Flag4Datas2 : font.Flag2Datas2);
                var chrdata = flagsdatas2[glyph.GlyphId];

                console.log(chrdata);

                if (chrdata.MeshPartIndex !== -1) {
                    var mdl = new grModel();
                    meshes = loadMeshPartFromAjax(mdl, flp.Model.Meshes[0], chrdata.MeshPartIndex);

                    if (chrdata.Materials && chrdata.Materials.length !== 0 && chrdata.Materials[0].TextureName) {
                        var txr_name = chrdata.Materials[0].TextureName;

                        if (!matmap.hasOwnProperty(txr_name)) {
                            var material = new grMaterial();
                            var img = flp.Textures[txr_name].Images[0].Image

                            var texture = new grTexture('data:image/png;base64,' + img);
                            texture.markAsFontTexture();
                            material.setDiffuse(texture);

                            matmap[txr_name] = material;
                        }
                        mdl.addMaterial(matmap[txr_name]);
                        for (var iMesh in meshes) {
                            meshes[iMesh].setMaterialID(0);
                        }
                    }

                    var matrix = mat4.fromTranslation(mat4.create(), [x, y, 0]);
                    mdl.matrix = mat4.scale(mat4.create(), matrix, [fontscale, fontscale, fontscale]);
                    gr_instance.models.push(mdl);
                }


                x += glyph.Width / 16;
            }

        }
        gr_instance.requestRedraw();
    });


});