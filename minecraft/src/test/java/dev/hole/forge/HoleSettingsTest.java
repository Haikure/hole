package dev.hole.forge;

import java.nio.file.*;
import org.junit.Test;
import static org.junit.Assert.*;

public class HoleSettingsTest {
    @Test public void keepsIdentityAndRejectsCorruptConfiguration() throws Exception {
        Path directory=Files.createTempDirectory("hole-config-test"),path=directory.resolve("hole.json");
        try {
            HoleSettings first=HoleSettings.load(path);
            assertEquals(first.deviceId,HoleSettings.load(path).deviceId);
            Files.writeString(path,"{\"password\":null}");
            try {HoleSettings.load(path);fail("accepted null secret");}catch(java.io.IOException expected){}
            assertEquals("{\"password\":null}",Files.readString(path));
        } finally {Files.deleteIfExists(path);Files.delete(directory);}
    }
}
