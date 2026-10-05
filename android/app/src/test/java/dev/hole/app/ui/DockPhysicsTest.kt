package dev.hole.app.ui

import kotlin.test.*
import org.junit.Test

class DockPhysicsTest {
    @Test fun retargetKeepsMomentumAndSettlesWithoutDrift() {
        val spring = DockSpring(0f)
        spring.to(4f, .12f, 0f)
        repeat(4) { spring.step(1f / 60) }
        val speed = spring.velocity
        val value = spring.value
        spring.to(1f)
        assertEquals(speed, spring.velocity)
        assertEquals(value, spring.value)
        repeat(600) { spring.step(1f / 60) }
        assertEquals(1f, spring.value)
        assertFalse(spring.active)
    }
    @Test fun fastLensStretchesAndLiftingKeepsItsCenter() {
        val rest = dockLensBounds(2f, 0f, 60f, 54f, 0f, 1f)
        val lifted = dockLensBounds(2f, 8f, 60f, 54f, 1f, 1f)
        assertEquals(rest.center, lifted.center)
        assertEquals(110f, lifted.width)
        assertEquals(71.75f, lifted.height)
    }
    @Test fun flingCanAdvanceAtMostOneExtraDestination() {
        assertEquals(3, dockFlingIndex(2f, 9000f, 60f, 2, 4))
        assertEquals(1, dockFlingIndex(2f, -9000f, 60f, 2, 4))
        assertEquals(4, dockFlingIndex(4f, 9000f, 60f, 4, 4))
        assertTrue(rubberBand(10f, .35f) < .35f)
        assertEquals(-rubberBand(10f, .35f), rubberBand(-10f, .35f))
    }
}
